import { describe, expect, test } from "bun:test";
import { getPublicKey } from "nostr-tools/pure";
import { hexToBytes } from "nostr-tools/utils";
import { v2 as nip44 } from "nostr-tools/nip44";
import { BuzzPersonFeed } from "../src/personal/buzz-person-feed.ts";
import type { PersonEventDelivery } from "../src/personal/gateway.ts";
import { MalformedRequest, parsePersonRequest, requireLoopbackEventsURL } from "../src/personal/parse.ts";
import { READ_STATE_CLIENT_ID, READ_STATE_TOPIC, readStateDTag, readStateSlotID, serializeReadStateBlob } from "../src/adapters/buzz/read-state.ts";
import type { BuzzRelayClient } from "../src/adapters/buzz/relay-client.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { UserConversation } from "../src/adapters/buzz/user-session.ts";

const aliceSecret = "1".repeat(64);
const bobSecret = "2".repeat(64);
const alice = getPublicKey(hexToBytes(aliceSecret));
const bob = getPublicKey(hexToBytes(bobSecret));
const carol = "c".repeat(64);
const eventsURL = "http://127.0.0.1:18091/events";
const startedAt = 1_800_000_000_000;

type Subscription = { filtersNow: () => object[]; onEvent: (event: BuzzEvent) => void; asked: number; isClosed: boolean };

type FakeRelay = BuzzRelayClient & { subscriptions: Subscription[]; queries: object[]; held: number; stored: BuzzEvent[] };

function fakeRelay(): FakeRelay {
	const relay: FakeRelay = {
		pubkeyHex: "",
		subscriptions: [],
		queries: [],
		held: 0,
		stored: [],
		connect: async () => {},
		disconnect: () => {},
		subscribe: (filtersNow, onEvent) => {
			const subscription: Subscription = { filtersNow, onEvent, asked: 1, isClosed: false };
			relay.subscriptions.push(subscription);
			return {
				askAgain: () => {
					subscription.asked += 1;
				},
				close: () => {
					subscription.isClosed = true;
				},
			};
		},
		query: async (filter) => {
			relay.queries.push(filter);
			const ids = (filter as { ids?: string[] }).ids ?? [];
			return relay.stored.filter((event) => ids.includes(event.id));
		},
		queryComplete: async () => ({ events: [], complete: true }),
		publish: async () => {
			throw new Error("a feed never publishes");
		},
		publishForAcknowledgement: async () => {
			throw new Error("a feed never publishes");
		},
	};
	return relay;
}

function conversation(channelID: string, participants: string[]): UserConversation {
	return { channelID, name: "", isDM: false, isPrivate: true, participantPubkeyHexes: participants };
}

function event(id: string, kind: number, author: string, tags: string[][], content = "", at = startedAt): BuzzEvent {
	return { id, pubkey: author, created_at: Math.floor(at / 1000), kind, tags, content, sig: "" };
}

function harness(conversationsBySecret: Map<string, UserConversation[]>) {
	const relays = new Map<string, FakeRelay>();
	const told: PersonEventDelivery[] = [];
	let listings = 0;
	let now = startedAt;
	const feed = new BuzzPersonFeed({
		holdRelay: (secret) => {
			const relay = relays.get(secret) ?? fakeRelay();
			relays.set(secret, relay);
			relay.held += 1;
			return { relay, connecting: Promise.resolve(), release: () => (relay.held -= 1) };
		},
		listConversations: async (secret) => {
			listings += 1;
			return conversationsBySecret.get(secret) ?? [];
		},
		tell: async (_url, delivery) => {
			told.push(delivery);
		},
		now: () => now,
	});
	const subscriptionOf = (secret: string) => relays.get(secret)!.subscriptions[0]!;
	const hear = async (secret: string, heard: BuzzEvent) => {
		subscriptionOf(secret).onEvent(heard);
		await Bun.sleep(0);
		await Bun.sleep(0);
	};
	return {
		feed,
		relays,
		told,
		subscriptionOf,
		hear,
		listings: () => listings,
		advance: (milliseconds: number) => {
			now += milliseconds;
		},
	};
}

describe("a person's feed", () => {
	test("asks for everything that changes in their conversations in one subscription", async () => {
		const { feed, relays, subscriptionOf } = harness(
			new Map([[aliceSecret, [conversation("group-1", [alice, bob]), conversation("dm-1", [alice, carol])]]]),
		);
		await feed.watch(aliceSecret, eventsURL);

		expect(relays.get(aliceSecret)!.subscriptions).toHaveLength(1);
		const filters = subscriptionOf(aliceSecret).filtersNow() as { kinds: number[]; "#h"?: string[] }[];
		expect(filters[0]).toMatchObject({ kinds: [9, 40003, 9005, 7, 20002], "#h": ["group-1", "dm-1"] });
		expect(filters.length).toBeLessThanOrEqual(10);
	});

	test("renewing a watch neither lists the conversations again nor asks the relay again", async () => {
		const { feed, subscriptionOf, listings } = harness(new Map([[aliceSecret, [conversation("group-1", [alice, bob])]]]));
		await feed.watch(aliceSecret, eventsURL);
		await feed.watch(aliceSecret, eventsURL);
		await feed.watch(aliceSecret, eventsURL);

		expect(listings()).toBe(1);
		expect(subscriptionOf(aliceSecret).asked).toBe(1);
	});

	test("tells a message once, to everyone in the conversation, even when every feed hears it", async () => {
		const group = conversation("group-1", [alice, bob]);
		const { feed, told, hear } = harness(
			new Map([
				[aliceSecret, [group]],
				[bobSecret, [group]],
			]),
		);
		await feed.watch(aliceSecret, eventsURL);
		await feed.watch(bobSecret, eventsURL);

		const sent = event("m-1", 9, alice, [["h", "group-1"]], "hello");
		await hear(aliceSecret, sent);
		await hear(bobSecret, sent);

		expect(told).toHaveLength(1);
		expect(told[0]!.recipientExternalIDs).toEqual([alice, bob]);
		expect(told[0]!.event).toMatchObject({
			kind: "message",
			message: { id: "m-1", conversationID: "group-1", authorExternalID: alice, body: "hello", reactions: [] },
		});
	});

	test("says an edit, a removal and a reaction as what they are, not as a message to read again", async () => {
		const { feed, told, hear, relays } = harness(new Map([[aliceSecret, [conversation("group-1", [alice, bob])]]]));
		await feed.watch(aliceSecret, eventsURL);
		const reaction = event("r-1", 7, bob, [["h", "group-1"], ["e", "m-1"]], "👍");
		relays.get(aliceSecret)!.stored.push(reaction);

		await hear(aliceSecret, event("x-1", 40003, alice, [["h", "group-1"], ["e", "m-1"]], "hello again"));
		await hear(aliceSecret, reaction);
		await hear(aliceSecret, event("d-1", 9005, bob, [["h", "group-1"], ["e", "r-1"], ["k", "7"]]));
		await hear(aliceSecret, event("d-2", 9005, alice, [["h", "group-1"], ["e", "m-1"]]));

		expect(told.map((delivery) => delivery.event)).toEqual([
			{ kind: "message.edited", conversationID: "group-1", messageID: "m-1", body: "hello again", editedAt: new Date(startedAt).toISOString() },
			{ kind: "reaction", conversationID: "group-1", messageID: "m-1", emoji: "👍", imageURL: undefined, externalID: bob, isAdded: true },
			{ kind: "reaction", conversationID: "group-1", messageID: "m-1", emoji: "👍", imageURL: undefined, externalID: bob, isAdded: false },
			{ kind: "message.removed", conversationID: "group-1", messageID: "m-1" },
		]);
	});

	test("tells typing to everyone but the one typing, and only while it is fresh", async () => {
		const { feed, told, hear, advance } = harness(new Map([[aliceSecret, [conversation("group-1", [alice, bob])]]]));
		await feed.watch(aliceSecret, eventsURL);

		await hear(aliceSecret, event("t-1", 20002, bob, [["h", "group-1"]]));
		advance(9_000);
		await hear(aliceSecret, event("t-2", 20002, bob, [["h", "group-1"]]));

		expect(told).toEqual([{ event: { kind: "typing", conversationID: "group-1", externalID: bob }, recipientExternalIDs: [alice] }]);
	});

	test("tells only the person when they read a conversation somewhere else", async () => {
		const { feed, told, hear } = harness(new Map([[aliceSecret, [conversation("group-1", [alice, bob])]]]));
		await feed.watch(aliceSecret, eventsURL);
		const blob = { clientID: READ_STATE_CLIENT_ID, readAtOfContext: new Map([["group-1", 1_800_000_100]]) };
		const key = nip44.utils.getConversationKey(hexToBytes(aliceSecret), alice);
		const tags = [["d", readStateDTag(readStateSlotID(alice))], ["t", READ_STATE_TOPIC]];

		await hear(aliceSecret, event("read-1", 30078, alice, tags, nip44.encrypt(serializeReadStateBlob(blob), key)));

		expect(told).toEqual([
			{ event: { kind: "read", readAtOfConversation: { "group-1": new Date(1_800_000_100_000).toISOString() } }, recipientExternalIDs: [alice] },
		]);
	});

	test("follows a conversation the person is added to by asking the same subscription again", async () => {
		const conversations = new Map([[aliceSecret, [conversation("group-1", [alice, bob])]]]);
		const { feed, told, hear, subscriptionOf, relays } = harness(conversations);
		await feed.watch(aliceSecret, eventsURL);

		conversations.set(aliceSecret, [conversation("group-1", [alice, bob]), conversation("group-2", [alice, carol])]);
		await hear(aliceSecret, event("roster-1", 39002, carol, [["d", "group-2"], ["p", alice], ["p", carol]]));
		await hear(aliceSecret, event("m-2", 9, carol, [["h", "group-2"]], "welcome"));

		expect(relays.get(aliceSecret)!.subscriptions).toHaveLength(1);
		expect(subscriptionOf(aliceSecret).asked).toBe(2);
		expect((subscriptionOf(aliceSecret).filtersNow()[0] as { "#h": string[] })["#h"]).toEqual(["group-1", "group-2"]);
		expect(told.map((delivery) => delivery.event.kind)).toEqual(["conversation", "message"]);
		expect(told[0]!.recipientExternalIDs).toEqual([alice, carol]);
	});

	test("asks again from just before the last event it heard, never from before it opened", async () => {
		const { feed, hear, subscriptionOf, advance } = harness(new Map([[aliceSecret, [conversation("group-1", [alice, bob])]]]));
		await feed.watch(aliceSecret, eventsURL);
		const sinceOf = () => (subscriptionOf(aliceSecret).filtersNow()[0] as { since: number }).since;
		expect(sinceOf()).toBe(startedAt / 1000);

		advance(10 * 60_000);
		await hear(aliceSecret, event("m-1", 9, bob, [["h", "group-1"]], "later", startedAt + 10 * 60_000));

		expect(sinceOf()).toBe(startedAt / 1000 + 10 * 60 - 60);
	});

	test("lets go of the socket of a person nobody renews", async () => {
		const { feed, relays, subscriptionOf, advance } = harness(
			new Map([
				[aliceSecret, [conversation("group-1", [alice, bob])]],
				[bobSecret, [conversation("group-1", [alice, bob])]],
			]),
		);
		await feed.watch(aliceSecret, eventsURL);
		advance(11 * 60_000);
		await feed.watch(bobSecret, eventsURL);

		expect(subscriptionOf(aliceSecret).isClosed).toBe(true);
		expect(relays.get(aliceSecret)!.held).toBe(0);
		expect(feed.watchedCount()).toBe(1);
	});
});

describe("where a feed is told", () => {
	const actor = { kind: "buzz-secret", secret: aliceSecret };

	test("is the relay on this machine", () => {
		expect(requireLoopbackEventsURL(parsePersonRequest({ actor, eventsURL }))).toBe(eventsURL);
	});

	test("is never an address off this machine", () => {
		for (const offered of ["http://relay.example.com/events", "https://127.0.0.1/events", "not a url"]) {
			expect(() => requireLoopbackEventsURL(parsePersonRequest({ actor, eventsURL: offered }))).toThrow(MalformedRequest);
		}
	});
});
