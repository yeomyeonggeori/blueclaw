import { getPublicKey } from "nostr-tools/pure";
import { hexToBytes } from "nostr-tools/utils";
import { v2 as nip44 } from "nostr-tools/nip44";
import { DELETE_MESSAGE_KIND, REACTION_KIND, isAbout } from "../adapters/buzz/deletions.ts";
import { READ_STATE_KIND, READ_STATE_TOPIC, isReadStateEvent } from "../adapters/buzz/read-state.ts";
import type { LiveSubscription } from "../adapters/buzz/relay-connection.ts";
import { holdRelayAs, type HeldRelay } from "../adapters/buzz/relay-pool.ts";
import { EDIT_MESSAGE_KIND, STREAM_MESSAGE_KIND, carriesTag, firstTagValue, type BuzzEvent } from "../adapters/buzz/types.ts";
import { decryptBlob } from "../adapters/buzz/user-read-state.ts";
import { reactionOf } from "../adapters/buzz/user-reactions.ts";
import { listUserConversations, userMessageOf, type UserConversation } from "../adapters/buzz/user-session.ts";
import { TYPING_INDICATOR_KIND } from "../adapters/buzz/user-typing.ts";
import { personalMessageOf } from "./buzz-message.ts";
import type { PersonEvent, PersonEventDelivery } from "./gateway.ts";

const GROUP_METADATA_KIND = 39000;
const GROUP_MEMBERS_KIND = 39002;
const MEMBER_ADDED_NOTIFICATION_KIND = 44100;
const MEMBER_REMOVED_NOTIFICATION_KIND = 44101;
const conversationKinds = [STREAM_MESSAGE_KIND, EDIT_MESSAGE_KIND, DELETE_MESSAGE_KIND, REACTION_KIND, TYPING_INDICATOR_KIND];
const rosterKinds = new Set([GROUP_METADATA_KIND, GROUP_MEMBERS_KIND, MEMBER_ADDED_NOTIFICATION_KIND, MEMBER_REMOVED_NOTIFICATION_KIND]);
const renewalWindowMilliseconds = 10 * 60_000;
const resumeOverlapSeconds = 60;
const typingFreshnessSeconds = 8;
const rememberedEventLimit = 10_000;

export type PersonFeedDependencies = {
	holdRelay: (userSecretHex: string) => HeldRelay;
	listConversations: (userSecretHex: string) => Promise<UserConversation[]>;
	tell: (eventsURL: string, delivery: PersonEventDelivery) => Promise<void>;
	now: () => number;
};

type Feed = {
	pubkey: string;
	userSecretHex: string;
	held: HeldRelay;
	ready: Promise<void>;
	subscription?: LiveSubscription;
	participantsByChannel: Map<string, string[]>;
	openedAtSeconds: number;
	heardUpToSeconds: number;
	renewedAt: number;
	relisting?: Promise<void>;
};

export function createBuzzPersonFeed(relayURL: string, authTagJSON: string | undefined): BuzzPersonFeed {
	return new BuzzPersonFeed({
		holdRelay: (userSecretHex) => holdRelayAs(relayURL, userSecretHex, authTagJSON),
		listConversations: (userSecretHex) => listUserConversations(relayURL, userSecretHex, { withProfiles: false, authTagJSON }),
		tell: postDelivery,
		now: () => Date.now(),
	});
}

// One subscription per person carries everything that changes in their
// conversations. It is asked for once the relay has let the person in, asked
// again from just before the last event heard whenever the socket reopens, and
// replaced only when the person's conversations themselves change.
export class BuzzPersonFeed {
	private readonly feeds = new Map<string, Feed>();
	private readonly toldEventIDs = new Set<string>();
	private telling: Promise<void> = Promise.resolve();
	private eventsURL = "";

	constructor(private readonly dependencies: PersonFeedDependencies) {}

	async watch(userSecretHex: string, eventsURL: string): Promise<void> {
		this.eventsURL = eventsURL;
		this.closeUnrenewed();
		const pubkey = getPublicKey(hexToBytes(userSecretHex));
		const feed = this.feeds.get(pubkey) ?? this.open(pubkey, userSecretHex);
		feed.renewedAt = this.dependencies.now();
		try {
			await feed.ready;
		} catch (reason) {
			this.close(feed);
			throw reason;
		}
	}

	watchedCount(): number {
		return this.feeds.size;
	}

	closeEvery(): void {
		for (const feed of [...this.feeds.values()]) this.close(feed);
	}

	private open(pubkey: string, userSecretHex: string): Feed {
		const held = this.dependencies.holdRelay(userSecretHex);
		const feed: Feed = {
			pubkey,
			userSecretHex,
			held,
			ready: Promise.resolve(),
			participantsByChannel: new Map(),
			openedAtSeconds: this.nowSeconds(),
			heardUpToSeconds: this.nowSeconds(),
			renewedAt: this.dependencies.now(),
		};
		feed.ready = held.connecting.then(() => this.follow(feed));
		this.feeds.set(pubkey, feed);
		return feed;
	}

	private close(feed: Feed): void {
		if (this.feeds.get(feed.pubkey) !== feed) return;
		this.feeds.delete(feed.pubkey);
		feed.subscription?.close();
		feed.held.release();
	}

	private async follow(feed: Feed): Promise<void> {
		const conversations = await this.dependencies.listConversations(feed.userSecretHex);
		feed.participantsByChannel = new Map(
			conversations.map((conversation) => [conversation.channelID, conversation.participantPubkeyHexes]),
		);
		if (feed.subscription) {
			feed.subscription.askAgain();
			return;
		}
		feed.subscription = feed.held.relay.subscribe(
			() => this.filtersOf(feed),
			(event) => this.heard(feed, event),
		);
	}

	private filtersOf(feed: Feed): object[] {
		const since = Math.max(feed.openedAtSeconds, feed.heardUpToSeconds - resumeOverlapSeconds);
		const channelIDs = [...feed.participantsByChannel.keys()];
		const ownFilters = [
			{ kinds: [GROUP_MEMBERS_KIND, MEMBER_ADDED_NOTIFICATION_KIND, MEMBER_REMOVED_NOTIFICATION_KIND], "#p": [feed.pubkey], since },
			{ kinds: [READ_STATE_KIND], authors: [feed.pubkey], "#t": [READ_STATE_TOPIC], since },
		];
		if (channelIDs.length === 0) return ownFilters;
		return [
			{ kinds: conversationKinds, "#h": channelIDs, since },
			{ kinds: [GROUP_METADATA_KIND, GROUP_MEMBERS_KIND], "#d": channelIDs, since },
			...ownFilters,
		];
	}

	private heard(feed: Feed, event: BuzzEvent): void {
		feed.heardUpToSeconds = Math.max(feed.heardUpToSeconds, event.created_at);
		if (rosterKinds.has(event.kind)) this.relist(feed);
		if (this.toldEventIDs.has(event.id)) return;
		this.remember(event.id);
		void this.deliveryOf(feed, event)
			.then((delivery) => {
				if (delivery) this.tell(delivery);
			})
			.catch((reason) => reportFeedFailure(`event ${event.id} for ${feed.pubkey}`, reason));
	}

	private async deliveryOf(feed: Feed, event: BuzzEvent): Promise<PersonEventDelivery | null> {
		if (event.kind === READ_STATE_KIND) return this.readStateDelivery(feed, event);
		if (rosterKinds.has(event.kind)) return this.conversationDelivery(feed, event);
		const channelID = firstTagValue(event, "h");
		if (!channelID) return null;
		const participants = feed.participantsByChannel.get(channelID);
		if (!participants) return null;
		const told = await this.conversationEventOf(feed, channelID, event);
		if (!told) return null;
		const recipientExternalIDs =
			told.kind === "typing" ? participants.filter((pubkey) => pubkey !== event.pubkey) : participants;
		return { event: told, recipientExternalIDs };
	}

	private async conversationEventOf(feed: Feed, channelID: string, event: BuzzEvent): Promise<PersonEvent | null> {
		const targetID = firstTagValue(event, "e") ?? "";
		if (event.kind === STREAM_MESSAGE_KIND) {
			return { kind: "message", message: personalMessageOf(userMessageOf(event, channelID)) };
		}
		if (event.kind === EDIT_MESSAGE_KIND) {
			return { kind: "message.edited", conversationID: channelID, messageID: targetID, body: event.content, editedAt: isoOf(event) };
		}
		if (event.kind === REACTION_KIND) return reactionEventOf(channelID, event, true);
		if (event.kind === DELETE_MESSAGE_KIND && isAbout(event, REACTION_KIND)) {
			const [reaction] = await feed.held.relay.query({ ids: [targetID], kinds: [REACTION_KIND] });
			return reaction ? reactionEventOf(channelID, reaction, false) : null;
		}
		if (event.kind === DELETE_MESSAGE_KIND) return { kind: "message.removed", conversationID: channelID, messageID: targetID };
		if (event.kind !== TYPING_INDICATOR_KIND || carriesTag(event, "e")) return null;
		if (this.nowSeconds() - event.created_at > typingFreshnessSeconds) return null;
		return { kind: "typing", conversationID: channelID, externalID: event.pubkey };
	}

	private readStateDelivery(feed: Feed, event: BuzzEvent): PersonEventDelivery | null {
		if (event.pubkey !== feed.pubkey || !isReadStateEvent(event)) return null;
		const secret = hexToBytes(feed.userSecretHex);
		const blob = decryptBlob(event.content, nip44.utils.getConversationKey(secret, feed.pubkey));
		if (!blob) return null;
		const readAtOfConversation = Object.fromEntries(
			[...blob.readAtOfContext].map(([conversationID, seconds]) => [conversationID, new Date(seconds * 1000).toISOString()]),
		);
		return { event: { kind: "read", readAtOfConversation }, recipientExternalIDs: [feed.pubkey] };
	}

	private conversationDelivery(feed: Feed, event: BuzzEvent): PersonEventDelivery | null {
		const conversationID = firstTagValue(event, "d") ?? firstTagValue(event, "h");
		if (!conversationID) return null;
		const participants = feed.participantsByChannel.get(conversationID) ?? [];
		const named = event.tags.filter((tag) => tag[0] === "p" && typeof tag[1] === "string").map((tag) => tag[1] as string);
		const recipientExternalIDs = [...new Set([feed.pubkey, ...participants, ...named])];
		return { event: { kind: "conversation", conversationID }, recipientExternalIDs };
	}

	private relist(feed: Feed): void {
		if (feed.relisting) return;
		feed.relisting = this.follow(feed)
			.catch((reason) => reportFeedFailure(`conversations of ${feed.pubkey}`, reason))
			.finally(() => {
				feed.relisting = undefined;
			});
	}

	private tell(delivery: PersonEventDelivery): void {
		const eventsURL = this.eventsURL;
		this.telling = this.telling
			.then(() => this.dependencies.tell(eventsURL, delivery))
			.catch((reason) => reportFeedFailure(`${delivery.event.kind} to ${eventsURL}`, reason));
	}

	private remember(eventID: string): void {
		this.toldEventIDs.add(eventID);
		if (this.toldEventIDs.size <= rememberedEventLimit) return;
		const oldest = this.toldEventIDs.values().next().value;
		if (oldest !== undefined) this.toldEventIDs.delete(oldest);
	}

	private closeUnrenewed(): void {
		const oldestRenewal = this.dependencies.now() - renewalWindowMilliseconds;
		for (const feed of [...this.feeds.values()]) {
			if (feed.renewedAt < oldestRenewal) this.close(feed);
		}
	}

	private nowSeconds(): number {
		return Math.floor(this.dependencies.now() / 1000);
	}
}

function reactionEventOf(channelID: string, reaction: BuzzEvent, isAdded: boolean): PersonEvent | null {
	const messageID = firstTagValue(reaction, "e");
	if (!messageID) return null;
	const { emoji, imageURL } = reactionOf(reaction);
	return { kind: "reaction", conversationID: channelID, messageID, emoji, imageURL, externalID: reaction.pubkey, isAdded };
}

function isoOf(event: BuzzEvent): string {
	return new Date(event.created_at * 1000).toISOString();
}

async function postDelivery(url: string, delivery: PersonEventDelivery): Promise<void> {
	const response = await fetch(url, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(delivery),
		signal: AbortSignal.timeout(10_000),
	});
	if (response.ok) return;
	throw new Error(`the relay answered ${response.status} at ${url}: ${await response.text()}`);
}

function reportFeedFailure(subject: string, reason: unknown): void {
	console.error("[person-feed]", subject, reason instanceof Error ? reason.message : String(reason));
}
