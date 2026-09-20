import { beforeEach, describe, expect, mock, test } from "bun:test";
import { getPublicKey } from "nostr-tools/pure";
import { hexToBytes } from "nostr-tools/utils";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_PUBKEY = "9".repeat(64);
const USER_SECRET = "2".repeat(64);
const AGENT_MESSAGE = "a".repeat(64);
const STRANGER_MESSAGE = "c".repeat(64);
const STRANGER = "e".repeat(64);
const PERSON = getPublicKey(hexToBytes(USER_SECRET));
const OTHER_CHANNEL = "3b7e1a90-5c2d-4e8f-9a1b-6c5d4e3f2a1b";
const OWN_REACTION = "b".repeat(64);
const STRANGER_REACTION = "d".repeat(64);
const PERSON_OTHER_REACTION = "f".repeat(64);

type Published = { kind: number; content: string; tags: string[][] };

let messages: BuzzEvent[] = [];
const publishedAsThePerson: Published[] = [];
const publishedAsTheAgent: Published[] = [];

function messageEvent(id: string, pubkey: string): BuzzEvent {
	return { id, pubkey, created_at: 100, kind: 9, tags: [["h", CHANNEL]], content: "먼저 쓴 글", sig: "" };
}

type Filter = { ids?: string[]; kinds?: number[]; authors?: string[]; "#e"?: string[] };

function eventsNamed(filter: Filter): BuzzEvent[] {
	if (filter.ids) return messages.filter((event) => filter.ids?.includes(event.id));
	if (!filter.kinds && !filter.authors && !filter["#e"]) return [];
	return messages.filter(
		(event) =>
			(!filter.kinds || filter.kinds.includes(event.kind)) &&
			(!filter.authors || filter.authors.includes(event.pubkey)) &&
			(!filter["#e"] || event.tags.some((tag) => tag[0] === "e" && filter["#e"]?.includes(tag[1] as string))),
	);
}

function reactionEvent(id: string, pubkey: string, emoji: string, channel = CHANNEL): BuzzEvent {
	return {
		id,
		pubkey,
		created_at: 100,
		kind: 7,
		tags: [["e", STRANGER_MESSAGE], ["h", channel]],
		content: emoji,
		sig: "",
	};
}

const personRelay = {
	pubkeyHex: "person",
	connect: async () => {},
	disconnect: () => {},
	subscribe: () => {},
	query: async (filter: Filter) => eventsNamed(filter),
	publish: async (kind: number, content: string, tags: string[][]) => {
		publishedAsThePerson.push({ kind, content, tags });
		return { id: "person-event", pubkey: "person", created_at: 300, kind, tags, content, sig: "" };
	},
	publishForAcknowledgement: async () => "",
};

mock.module("../src/adapters/buzz/relay-client.ts", () => ({ createBuzzRelayClient: () => personRelay }));

const { BuzzAdapter } = await import("../src/adapters/buzz/adapter.ts");
const { createBuzzPersonalGateway } = await import("../src/personal/buzz.ts");

function gatewayOverTheRelay() {
	const adapter = new BuzzAdapter({
		relayURL: "wss://relay.test",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
	(adapter as unknown as { relay: unknown }).relay = {
		pubkeyHex: BOT_PUBKEY,
		query: async (filter: Filter) => eventsNamed(filter),
		publish: async (kind: number, content: string, tags: string[][]) => {
			publishedAsTheAgent.push({ kind, content, tags });
			return { id: "agent-event", pubkey: BOT_PUBKEY, created_at: 300, kind, tags, content, sig: "" };
		},
	};
	return createBuzzPersonalGateway(adapter, { relayURL: "wss://relay.test" });
}

const actor = { kind: "buzz-secret", secret: USER_SECRET };

beforeEach(() => {
	messages = [messageEvent(AGENT_MESSAGE, BOT_PUBKEY), messageEvent(STRANGER_MESSAGE, STRANGER)];
	publishedAsThePerson.length = 0;
	publishedAsTheAgent.length = 0;
});

describe("a person changing a message through the personal API", () => {
	test("an edit to the agent's message is published by the agent", async () => {
		await gatewayOverTheRelay().editMessage(actor, CHANNEL, AGENT_MESSAGE, "고친 글");

		expect(publishedAsTheAgent.map((event) => event.kind)).toEqual([40003]);
		expect(publishedAsThePerson).toEqual([]);
	});

	test("an edit to somebody else's message stays the person's own event", async () => {
		await gatewayOverTheRelay().editMessage(actor, CHANNEL, STRANGER_MESSAGE, "고친 글");

		expect(publishedAsThePerson.map((event) => event.kind)).toEqual([40003]);
		expect(publishedAsTheAgent).toEqual([]);
	});

	test("a deletion of the agent's message is published by the agent", async () => {
		await gatewayOverTheRelay().deleteMessage(actor, CHANNEL, AGENT_MESSAGE);

		expect(publishedAsTheAgent.map((event) => event.kind)).toEqual([9005]);
		expect(publishedAsThePerson).toEqual([]);
	});

	test("a deletion of somebody else's message stays the person's own event", async () => {
		await gatewayOverTheRelay().deleteMessage(actor, CHANNEL, STRANGER_MESSAGE);

		expect(publishedAsThePerson.map((event) => event.kind)).toEqual([9005]);
		expect(publishedAsTheAgent).toEqual([]);
	});
});

describe("a person taking a reaction back on buzz", () => {
	test("deletes the person's own reaction and nobody else's", async () => {
		messages.push(reactionEvent(OWN_REACTION, PERSON, "👀"), reactionEvent(STRANGER_REACTION, STRANGER, "👀"));

		await gatewayOverTheRelay().removeReaction(actor, CHANNEL, STRANGER_MESSAGE, "👀");

		expect(publishedAsThePerson.map((event) => event.kind)).toEqual([9005]);
		expect(publishedAsThePerson[0]?.tags).toContainEqual(["e", OWN_REACTION]);
		expect(publishedAsThePerson[0]?.tags).not.toContainEqual(["e", STRANGER_REACTION]);
	});

	test("stamps the channel the reaction lives in, not the one it was asked from", async () => {
		messages.push(reactionEvent(OWN_REACTION, PERSON, "👀", OTHER_CHANNEL));

		await gatewayOverTheRelay().removeReaction(actor, CHANNEL, STRANGER_MESSAGE, "👀");

		expect(publishedAsThePerson[0]?.tags).toContainEqual(["h", OTHER_CHANNEL]);
	});

	test("takes back only the emoji that was asked for", async () => {
		messages.push(reactionEvent(OWN_REACTION, PERSON, "🚀"), reactionEvent(PERSON_OTHER_REACTION, PERSON, "👀"));

		await gatewayOverTheRelay().removeReaction(actor, CHANNEL, STRANGER_MESSAGE, "👀");

		expect(publishedAsThePerson[0]?.tags).toContainEqual(["e", PERSON_OTHER_REACTION]);
		expect(publishedAsThePerson[0]?.tags).not.toContainEqual(["e", OWN_REACTION]);
	});

	test("publishes nothing when the person never gave that reaction", async () => {
		messages.push(reactionEvent(STRANGER_REACTION, STRANGER, "👀"));

		await gatewayOverTheRelay().removeReaction(actor, CHANNEL, STRANGER_MESSAGE, "👀");

		expect(publishedAsThePerson).toEqual([]);
		expect(publishedAsTheAgent).toEqual([]);
	});
});
