import { createBuzzRelayClient, type BuzzRelayClient } from "../adapters/buzz/relay-client.ts";
import { listUserConversations, pubkeyFromSecret, type UserConversation } from "../adapters/buzz/user-session.ts";
import { firstTagValue, type BuzzEvent } from "../adapters/buzz/types.ts";

const STREAM_MESSAGE_KIND = 9;
const MEMBER_ADDED_NOTIFICATION_KIND = 44100;
const renewalWindowMilliseconds = 10 * 60_000;
const freshnessSeconds = 10 * 60;
const rememberedMessageLimit = 10_000;

export type Arrival = {
	conversationID: string;
	messageID: string;
	authorExternalID: string;
	recipientExternalIDs: string[];
	preview: string;
};

export type ArrivalWatchDependencies = {
	openRelay: (userSecretHex: string) => BuzzRelayClient;
	listConversations: (userSecretHex: string) => Promise<UserConversation[]>;
	tell: (arrivalsURL: string, arrival: Arrival) => Promise<void>;
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
		tell: postArrival,
		now: () => Date.now(),
	});
}

export class BuzzArrivalWatch {
	private readonly watchers = new Map<string, Watcher>();
	private readonly toldMessageIDs = new Set<string>();
	private arrivalsURL = "";

	constructor(private readonly dependencies: ArrivalWatchDependencies) {}

	async watch(userSecretHex: string, arrivalsURL: string): Promise<void> {
		this.arrivalsURL = arrivalsURL;
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
				[{ kinds: [STREAM_MESSAGE_KIND], "#h": joinedChannelIDs, since: watcher.listedAtSeconds }],
				(event) => this.arrived(watcher, event),
			);
		}
		watcher.listedAtSeconds = listingStartedAt;
	}

	private arrived(watcher: Watcher, event: BuzzEvent): void {
		const channelID = firstTagValue(event, "h");
		if (!channelID || !watcher.participantsByChannel.has(channelID)) return;
		if (event.kind !== STREAM_MESSAGE_KIND || this.toldMessageIDs.has(event.id)) return;
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

	private remember(messageID: string): void {
		this.toldMessageIDs.add(messageID);
		if (this.toldMessageIDs.size <= rememberedMessageLimit) return;
		const oldest = this.toldMessageIDs.values().next().value;
		if (oldest !== undefined) this.toldMessageIDs.delete(oldest);
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

async function postArrival(arrivalsURL: string, arrival: Arrival): Promise<void> {
	const response = await fetch(arrivalsURL, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(arrival),
		signal: AbortSignal.timeout(10_000),
	});
	if (response.ok) return;
	throw new Error(`the relay answered ${response.status} to an arrival at ${arrivalsURL}: ${await response.text()}`);
}

function reportWatchFailure(subject: string, reason: unknown): void {
	console.error("[arrival-watch]", subject, reason instanceof Error ? reason.message : String(reason));
}
