import { describe, expect, test } from "bun:test";
import { BuzzArrivalWatch, type Arrival, type Typing } from "../src/personal/buzz-arrival-watch.ts";
import {
	MalformedRequest,
	optionalLoopbackTypingURL,
	parsePersonRequest,
	requireLoopbackArrivalsURL,
} from "../src/personal/parse.ts";
import type { BuzzRelayClient } from "../src/adapters/buzz/relay-client.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { UserConversation } from "../src/adapters/buzz/user-session.ts";

const aliceSecret = "1".repeat(64);
const bobSecret = "2".repeat(64);
const arrivalsURL = "http://127.0.0.1:18091/arrived";
const typingURL = "http://127.0.0.1:18091/typing";
const startedAt = 1_800_000_000_000;

type FakeRelay = BuzzRelayClient & {
	subscriptions: { filters: object[]; onEvent: (event: BuzzEvent) => void }[];
	connections: number;
	disconnected: boolean;
};

function fakeRelay(): FakeRelay {
	const relay: FakeRelay = {
		pubkeyHex: "",
		subscriptions: [],
		connections: 0,
		disconnected: false,
		connect: async () => {
			relay.connections += 1;
		},
		disconnect: () => {
			relay.disconnected = true;
		},
		subscribe: (filters, onEvent) => {
			relay.subscriptions.push({ filters, onEvent });
		},
		query: async () => [],
		queryComplete: async () => ({ events: [], complete: true }),
		publish: async () => {
			throw new Error("a watcher never publishes");
		},
		publishForAcknowledgement: async () => {
			throw new Error("a watcher never publishes");
		},
	};
	return relay;
}

function conversation(channelID: string, participants: string[]): UserConversation {
	return { channelID, name: "", isDM: true, isPrivate: true, participantPubkeyHexes: participants };
}

function message(id: string, channelID: string, author: string, createdAtMilliseconds: number): BuzzEvent {
	return {
		id,
		pubkey: author,
		created_at: Math.floor(createdAtMilliseconds / 1000),
		kind: 9,
		tags: [["h", channelID]],
		content: `hello from ${author}`,
		sig: "",
	};
}

function typing(id: string, channelID: string, author: string, createdAtMilliseconds: number): BuzzEvent {
	return { ...message(id, channelID, author, createdAtMilliseconds), kind: 20002, content: "" };
}

function deliverTo(relay: FakeRelay, channelID: string, event: BuzzEvent): void {
	for (const subscription of relay.subscriptions) {
		const [filter] = subscription.filters as { "#h"?: string[] }[];
		if (filter?.["#h"]?.includes(channelID)) subscription.onEvent(event);
	}
}

function harness(conversationsBySecret: Map<string, UserConversation[]>) {
	const relays = new Map<string, FakeRelay>();
	const told: Arrival[] = [];
	const typed: Typing[] = [];
	let now = startedAt;
	const watch = new BuzzArrivalWatch({
		openRelay: (secret) => {
			const relay = fakeRelay();
			relays.set(secret, relay);
			return relay;
		},
		listConversations: async (secret) => conversationsBySecret.get(secret) ?? [],
		tell: async (_url, arrival) => {
			told.push(arrival);
		},
		tellTyping: async (_url, typing) => {
			typed.push(typing);
		},
		now: () => now,
	});
	return {
		watch,
		relays,
		told,
		typed,
		advance: (milliseconds: number) => {
			now += milliseconds;
		},
		now: () => now,
	};
}

describe("watching a person's conversations for arriving messages", () => {
	test("tells a message once even when every participant's watcher sees it", async () => {
		const dm = conversation("dm-1", ["alice", "bob"]);
		const { watch, relays, told, now } = harness(
			new Map([
				[aliceSecret, [dm]],
				[bobSecret, [dm]],
			]),
		);
		await watch.watch(aliceSecret, arrivalsURL);
		await watch.watch(bobSecret, arrivalsURL);

		const sent = message("m-1", "dm-1", "alice", now());
		deliverTo(relays.get(aliceSecret)!, "dm-1", sent);
		deliverTo(relays.get(bobSecret)!, "dm-1", sent);
		deliverTo(relays.get(bobSecret)!, "dm-1", sent);

		expect(told).toEqual([
			{
				conversationID: "dm-1",
				messageID: "m-1",
				authorExternalID: "alice",
				recipientExternalIDs: ["alice", "bob"],
				preview: "hello from alice",
			},
		]);
	});

	test("renewing a watch keeps one connection and does not subscribe a channel twice", async () => {
		const { watch, relays } = harness(new Map([[aliceSecret, [conversation("dm-1", ["alice", "bob"])]]]));
		await watch.watch(aliceSecret, arrivalsURL);
		await watch.watch(aliceSecret, arrivalsURL);

		const relay = relays.get(aliceSecret)!;
		expect(relay.connections).toBe(1);
		const channelSubscriptions = relay.subscriptions.filter((one) =>
			(one.filters as { "#h"?: string[] }[])[0]?.["#h"],
		);
		expect(channelSubscriptions).toHaveLength(1);
	});

	test("every conversation a person is in shares one subscription", async () => {
		const { watch, relays } = harness(
			new Map([
				[
					aliceSecret,
					[conversation("dm-1", ["alice", "bob"]), conversation("dm-2", ["alice", "carol"]), conversation("group-1", ["alice", "bob", "carol"])],
				],
			]),
		);
		await watch.watch(aliceSecret, arrivalsURL);

		const channelFilters = relays
			.get(aliceSecret)!
			.subscriptions.map((one) => (one.filters as { "#h"?: string[] }[])[0]?.["#h"])
			.filter((channelIDs) => channelIDs !== undefined);
		expect(channelFilters).toEqual([["dm-1", "dm-2", "group-1"]]);
	});

	test("a conversation joined later is followed from the previous listing, so its first message is not missed", async () => {
		const conversations = new Map([[aliceSecret, [conversation("dm-1", ["alice", "bob"])]]]);
		const { watch, relays, told, advance, now } = harness(conversations);
		await watch.watch(aliceSecret, arrivalsURL);
		const firstListing = Math.floor(now() / 1000);

		advance(60_000);
		const openedBetweenListings = message("m-2", "dm-2", "carol", now());
		advance(60_000);
		conversations.set(aliceSecret, [conversation("dm-1", ["alice", "bob"]), conversation("dm-2", ["alice", "carol"])]);
		await watch.watch(aliceSecret, arrivalsURL);

		const relay = relays.get(aliceSecret)!;
		const joined = relay.subscriptions.find((one) => (one.filters as { "#h"?: string[] }[])[0]?.["#h"]?.includes("dm-2"));
		expect((joined?.filters[0] as { since: number }).since).toBe(firstListing);
		deliverTo(relay, "dm-2", openedBetweenListings);
		expect(told.map((arrival) => arrival.messageID)).toEqual(["m-2"]);
	});

	test("a conversation the person has left is not told, and rejoining it does not subscribe again", async () => {
		const conversations = new Map([[aliceSecret, [conversation("group-1", ["alice", "bob"])]]]);
		const { watch, relays, told, now } = harness(conversations);
		await watch.watch(aliceSecret, arrivalsURL);
		const relay = relays.get(aliceSecret)!;

		conversations.set(aliceSecret, []);
		await watch.watch(aliceSecret, arrivalsURL);
		deliverTo(relay, "group-1", message("m-after-leaving", "group-1", "bob", now()));
		expect(told).toHaveLength(0);

		conversations.set(aliceSecret, [conversation("group-1", ["alice", "bob", "carol"])]);
		await watch.watch(aliceSecret, arrivalsURL);
		deliverTo(relay, "group-1", message("m-after-rejoining", "group-1", "bob", now()));

		const channelSubscriptions = relay.subscriptions.filter((one) => (one.filters as { "#h"?: string[] }[])[0]?.["#h"]);
		expect(channelSubscriptions).toHaveLength(1);
		expect(told.map((arrival) => [arrival.messageID, arrival.recipientExternalIDs])).toEqual([
			["m-after-rejoining", ["alice", "bob", "carol"]],
		]);
	});

	test("a message replayed long after it was posted is not told", async () => {
		const { watch, relays, told, now } = harness(new Map([[aliceSecret, [conversation("dm-1", ["alice", "bob"])]]]));
		await watch.watch(aliceSecret, arrivalsURL);

		deliverTo(relays.get(aliceSecret)!, "dm-1", message("m-old", "dm-1", "bob", now() - 60 * 60_000));

		expect(told).toHaveLength(0);
	});

	test("a watch nobody renews is closed when another is renewed", async () => {
		const { watch, relays, advance } = harness(
			new Map([
				[aliceSecret, [conversation("dm-1", ["alice", "bob"])]],
				[bobSecret, [conversation("dm-1", ["alice", "bob"])]],
			]),
		);
		await watch.watch(aliceSecret, arrivalsURL);
		advance(11 * 60_000);
		await watch.watch(bobSecret, arrivalsURL);

		expect(relays.get(aliceSecret)!.disconnected).toBe(true);
		expect(watch.watchedCount()).toBe(1);
	});
});

describe("where arrivals are told", () => {
	const actor = { kind: "buzz-secret", secret: aliceSecret };

	test("is the relay on this machine", () => {
		expect(requireLoopbackArrivalsURL(parsePersonRequest({ actor, arrivalsURL }))).toBe(arrivalsURL);
	});

	test("is never an address off this machine", () => {
		for (const offered of ["http://relay.example.com/arrived", "https://127.0.0.1/arrived", "not a url"]) {
			expect(() => requireLoopbackArrivalsURL(parsePersonRequest({ actor, arrivalsURL: offered }))).toThrow(MalformedRequest);
		}
	});
});

describe("watching a person's conversations for someone typing", () => {
	test("tells typing once, to everyone in the conversation, and never as a message", async () => {
		const dm = conversation("dm-1", ["alice", "bob"]);
		const { watch, relays, told, typed, now } = harness(
			new Map([
				[aliceSecret, [dm]],
				[bobSecret, [dm]],
			]),
		);
		await watch.watch(aliceSecret, arrivalsURL, typingURL);
		await watch.watch(bobSecret, arrivalsURL, typingURL);

		const heard = typing("t-1", "dm-1", "alice", now());
		deliverTo(relays.get(aliceSecret)!, "dm-1", heard);
		deliverTo(relays.get(bobSecret)!, "dm-1", heard);

		expect(typed).toEqual([
			{ conversationID: "dm-1", authorExternalID: "alice", recipientExternalIDs: ["alice", "bob"] },
		]);
		expect(told).toEqual([]);
	});

	test("a conversation opened after the watch started is followed when the relay says the person was added, so its first typing is told", async () => {
		const conversations = new Map([[aliceSecret, [conversation("dm-1", ["alice", "bob"])]]]);
		const { watch, relays, typed, now } = harness(conversations);
		await watch.watch(aliceSecret, arrivalsURL, typingURL);
		const relay = relays.get(aliceSecret)!;

		conversations.set(aliceSecret, [conversation("dm-1", ["alice", "bob"]), conversation("dm-2", ["alice", "agent"])]);
		const [membership] = relay.subscriptions.filter(({ filters }) =>
			(filters as { kinds?: number[] }[])[0]?.kinds?.includes(44100),
		);
		membership!.onEvent({ ...message("added-1", "dm-2", "relay", now()), kind: 44100, tags: [["p", "alice"], ["h", "dm-2"]] });
		await Bun.sleep(0);
		deliverTo(relay, "dm-2", typing("t-1", "dm-2", "agent", now()));

		expect(typed).toEqual([{ conversationID: "dm-2", authorExternalID: "agent", recipientExternalIDs: ["alice", "agent"] }]);
	});

	test("typing older than the indicator lasts is not told", async () => {
		const { watch, relays, typed, now } = harness(new Map([[aliceSecret, [conversation("dm-1", ["alice", "bob"])]]]));
		await watch.watch(aliceSecret, arrivalsURL, typingURL);

		deliverTo(relays.get(aliceSecret)!, "dm-1", typing("t-1", "dm-1", "bob", now() - 9_000));

		expect(typed).toEqual([]);
	});

	test("typing inside a thread is not told as typing in the conversation", async () => {
		const { watch, relays, typed, now } = harness(new Map([[aliceSecret, [conversation("dm-1", ["alice", "bob"])]]]));
		await watch.watch(aliceSecret, arrivalsURL, typingURL);

		const inThread = typing("t-1", "dm-1", "bob", now());
		deliverTo(relays.get(aliceSecret)!, "dm-1", { ...inThread, tags: [...inThread.tags, ["e", "root-1", "", "reply"]] });

		expect(typed).toEqual([]);
	});

	test("a relay that names nowhere to tell typing is not asked to watch for it", async () => {
		const { watch, relays, typed, now } = harness(new Map([[aliceSecret, [conversation("dm-1", ["alice", "bob"])]]]));
		await watch.watch(aliceSecret, arrivalsURL);

		const [subscription] = relays.get(aliceSecret)!.subscriptions.filter(({ filters }) =>
			(filters as { "#h"?: string[] }[])[0]?.["#h"]?.includes("dm-1"),
		);
		expect((subscription!.filters as { kinds: number[] }[])[0]!.kinds).toEqual([9]);
		deliverTo(relays.get(aliceSecret)!, "dm-1", typing("t-1", "dm-1", "bob", now()));
		expect(typed).toEqual([]);
	});
});

describe("where typing is told", () => {
	const actor = { kind: "buzz-secret", secret: aliceSecret };

	test("is the relay on this machine, when the relay names a place", () => {
		expect(optionalLoopbackTypingURL(parsePersonRequest({ actor, typingURL }))).toBe(typingURL);
		expect(optionalLoopbackTypingURL(parsePersonRequest({ actor }))).toBeUndefined();
	});

	test("is never an address off this machine", () => {
		expect(() =>
			optionalLoopbackTypingURL(parsePersonRequest({ actor, typingURL: "http://relay.example.com/typing" })),
		).toThrow(MalformedRequest);
	});
});
