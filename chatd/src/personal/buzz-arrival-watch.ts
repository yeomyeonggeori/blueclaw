import { createBuzzRelayClient, type BuzzRelayClient } from "../adapters/buzz/relay-client.ts";
import { listUserConversations, pubkeyFromSecret, type UserConversation } from "../adapters/buzz/user-session.ts";
import { carriesTag, firstTagValue, type BuzzEvent } from "../adapters/buzz/types.ts";
import { TYPING_INDICATOR_KIND } from "../adapters/buzz/user-typing.ts";

const STREAM_MESSAGE_KIND = 9;
const MEMBER_ADDED_NOTIFICATION_KIND = 44100;
const renewalWindowMilliseconds = 10 * 60_000;
const freshnessSeconds = 10 * 60;
const typingFreshnessSeconds = 8;
const rememberedEventLimit = 10_000;

export type Arrival = {
	conversationID: string;
	messageID: string;
	authorExternalID: string;
	recipientExternalIDs: string[];
	preview: string;
};

export type Typing = {
	conversationID: string;
	authorExternalID: string;
	recipientExternalIDs: string[];
};

export type ArrivalWatchDependencies = {
	openRelay: (userSecretHex: string) => BuzzRelayClient;
	listConversations: (userSecretHex: string) => Promise<UserConversation[]>;
	tell: (arrivalsURL: string, arrival: Arrival) => Promise<void>;
	tellTyping: (typingURL: string, typing: Typing) => Promise<void>;
	now: () => number;
};

type Watcher = {
	userSecretHex: string;
	relay: BuzzRelayClient;
	connecting: Promise<void>;
	participantsByChannel: Map<string, string[]>;
	subscribedChannelIDs: Set<string>;
	listedAtSeconds: number;
	renewedAt: number;
};

export function createBuzzArrivalWatch(relayURL: string, authTagJSON: string | undefined): BuzzArrivalWatch {
	return new BuzzArrivalWatch({
		openRelay: (userSecretHex) => createBuzzRelayClient(relayURL, userSecretHex, authTagJSON),
		listConversations: (userSecretHex) => listUserConversations(relayURL, userSecretHex, { withProfiles: false }),
		tell: postToTheRelay,
		tellTyping: postToTheRelay,
		now: () => Date.now(),
	});
}

export class BuzzArrivalWatch {
	private readonly watchers = new Map<string, Watcher>();
	private readonly toldEventIDs = new Set<string>();
	private arrivalsURL = "";
	private typingURL = "";

	constructor(private readonly dependencies: ArrivalWatchDependencies) {}

	async watch(userSecretHex: string, arrivalsURL: string, typingURL = ""): Promise<void> {
		this.arrivalsURL = arrivalsURL;
		this.typingURL = typingURL;
		this.closeUnrenewed();
		const pubkey = pubkeyFromSecret(userSecretHex);
		const watcher = this.watchers.get(pubkey) ?? this.open(pubkey, userSecretHex);
		watcher.renewedAt = this.dependencies.now();
		try {
			await watcher.connecting;
		} catch (reason) {
			this.close(pubkey, watcher);
			throw reason;
		}
		await this.followConversations(watcher);
	}

	watchedCount(): number {
		return this.watchers.size;
	}

	closeEvery(): void {
		for (const [pubkey, watcher] of this.watchers) this.close(pubkey, watcher);
	}

	private close(pubkey: string, watcher: Watcher): void {
		if (this.watchers.get(pubkey) !== watcher) return;
		this.watchers.delete(pubkey);
		watcher.relay.disconnect();
	}

	private open(pubkey: string, userSecretHex: string): Watcher {
		const relay = this.dependencies.openRelay(userSecretHex);
		const openedAtSeconds = this.nowSeconds();
		const watcher: Watcher = {
			userSecretHex,
			relay,
			connecting: relay.connect().then(() =>
				relay.subscribe(
					[{ kinds: [MEMBER_ADDED_NOTIFICATION_KIND], "#p": [pubkey], since: openedAtSeconds }],
					() => {
						void this.followConversations(watcher).catch((reason) => reportWatchFailure(pubkey, reason));
					},
				),
			),
			participantsByChannel: new Map(),
			subscribedChannelIDs: new Set(),
			listedAtSeconds: openedAtSeconds,
			renewedAt: this.dependencies.now(),
		};
		this.watchers.set(pubkey, watcher);
		return watcher;
	}

	private async followConversations(watcher: Watcher): Promise<void> {
		const listingStartedAt = this.nowSeconds();
		const conversations = await this.dependencies.listConversations(watcher.userSecretHex);
		watcher.participantsByChannel = new Map(
			conversations.map((conversation) => [conversation.channelID, conversation.participantPubkeyHexes]),
		);
		const joinedChannelIDs = [...watcher.participantsByChannel.keys()].filter(
			(channelID) => !watcher.subscribedChannelIDs.has(channelID),
		);
		for (const channelID of joinedChannelIDs) watcher.subscribedChannelIDs.add(channelID);
		if (joinedChannelIDs.length > 0) {
			watcher.relay.subscribe(
				[{ kinds: this.watchedKinds(), "#h": joinedChannelIDs, since: watcher.listedAtSeconds }],
				(event) => this.heard(watcher, event),
			);
		}
		watcher.listedAtSeconds = listingStartedAt;
	}

	private watchedKinds(): number[] {
		return this.typingURL ? [STREAM_MESSAGE_KIND, TYPING_INDICATOR_KIND] : [STREAM_MESSAGE_KIND];
	}

	private heard(watcher: Watcher, event: BuzzEvent): void {
		const channelID = firstTagValue(event, "h");
		if (!channelID || !watcher.participantsByChannel.has(channelID)) return;
		if (this.toldEventIDs.has(event.id)) return;
		if (event.kind === STREAM_MESSAGE_KIND) this.arrived(watcher, channelID, event);
		if (event.kind === TYPING_INDICATOR_KIND) this.typed(watcher, channelID, event);
	}

	private typed(watcher: Watcher, channelID: string, event: BuzzEvent): void {
		if (!this.typingURL || carriesTag(event, "e")) return;
		if (this.nowSeconds() - event.created_at > typingFreshnessSeconds) return;
		this.remember(event.id);
		const typing: Typing = {
			conversationID: channelID,
			authorExternalID: event.pubkey,
			recipientExternalIDs: watcher.participantsByChannel.get(channelID) ?? [],
		};
		void this.dependencies
			.tellTyping(this.typingURL, typing)
			.catch((reason) => reportWatchFailure(`typing by ${event.pubkey} in ${channelID}`, reason));
	}

	private arrived(watcher: Watcher, channelID: string, event: BuzzEvent): void {
		if (this.nowSeconds() - event.created_at > freshnessSeconds) return;
		this.remember(event.id);
		const arrival: Arrival = {
			conversationID: channelID,
			messageID: event.id,
			authorExternalID: event.pubkey,
			recipientExternalIDs: watcher.participantsByChannel.get(channelID) ?? [],
			preview: event.content,
		};
		void this.dependencies
			.tell(this.arrivalsURL, arrival)
			.catch((reason) => reportWatchFailure(`message ${event.id} in ${channelID}`, reason));
	}

	private remember(eventID: string): void {
		this.toldEventIDs.add(eventID);
		if (this.toldEventIDs.size <= rememberedEventLimit) return;
		const oldest = this.toldEventIDs.values().next().value;
		if (oldest !== undefined) this.toldEventIDs.delete(oldest);
	}

	private closeUnrenewed(): void {
		const oldestRenewal = this.dependencies.now() - renewalWindowMilliseconds;
		for (const [pubkey, watcher] of this.watchers) {
			if (watcher.renewedAt < oldestRenewal) this.close(pubkey, watcher);
		}
	}

	private nowSeconds(): number {
		return Math.floor(this.dependencies.now() / 1000);
	}
}

async function postToTheRelay(url: string, heard: Arrival | Typing): Promise<void> {
	const response = await fetch(url, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(heard),
		signal: AbortSignal.timeout(10_000),
	});
	if (response.ok) return;
	throw new Error(`the relay answered ${response.status} at ${url}: ${await response.text()}`);
}

function reportWatchFailure(subject: string, reason: unknown): void {
	console.error("[arrival-watch]", subject, reason instanceof Error ? reason.message : String(reason));
}
