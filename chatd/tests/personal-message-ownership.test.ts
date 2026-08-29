import { beforeEach, describe, expect, mock, test } from "bun:test";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_PUBKEY = "9".repeat(64);
const USER_SECRET = "2".repeat(64);
const AGENT_MESSAGE = "a".repeat(64);
const STRANGER_MESSAGE = "c".repeat(64);
const STRANGER = "e".repeat(64);

type Published = { kind: number; content: string; tags: string[][] };

let messages: BuzzEvent[] = [];
const publishedAsThePerson: Published[] = [];
const publishedAsTheAgent: Published[] = [];

function messageEvent(id: string, pubkey: string): BuzzEvent {
	return { id, pubkey, created_at: 100, kind: 9, tags: [["h", CHANNEL]], content: "먼저 쓴 글", sig: "" };
}

function eventsNamed(filter: { ids?: string[] }): BuzzEvent[] {
	if (!filter.ids) return [];
	return messages.filter((event) => filter.ids?.includes(event.id));
}

const personRelay = {
	pubkeyHex: "person",
	connect: async () => {},
	disconnect: () => {},
	subscribe: () => {},
	query: async (filter: { ids?: string[] }) => eventsNamed(filter),
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
		query: async (filter: { ids?: string[] }) => eventsNamed(filter),
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
