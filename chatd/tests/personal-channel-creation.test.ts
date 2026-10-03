import { beforeEach, describe, expect, mock, test } from "bun:test";

const BOT_PUBKEY = "9".repeat(64);
const USER_SECRET = "2".repeat(64);
const COLLEAGUE = "e".repeat(64);
const PUT_USER_KIND = 9000;

type Published = { kind: number; content: string; tags: string[][] };

const publishedAsThePerson: Published[] = [];

const personRelay = {
	pubkeyHex: "person",
	connect: async () => {},
	disconnect: () => {},
	subscribe: () => {},
	query: async () => [],
	queryComplete: async () => ({ events: [], complete: true }),
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
		relayURL: "wss://channel-creation.test",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
	(adapter as unknown as { relay: unknown }).relay = { pubkeyHex: BOT_PUBKEY };
	return createBuzzPersonalGateway(adapter, { relayURL: "wss://channel-creation.test" });
}

function addedMembers(): string[] {
	return publishedAsThePerson
		.filter((event) => event.kind === PUT_USER_KIND)
		.map((event) => event.tags.find((tag) => tag[0] === "p")?.[1] ?? "");
}

const actor = { kind: "buzz-secret", secret: USER_SECRET };

beforeEach(() => {
	publishedAsThePerson.length = 0;
});

describe("a person creating a channel on buzz", () => {
	test("an open channel takes the agent in beside the members the person chose", async () => {
		await gatewayOverTheRelay().createChannel(actor, {
			name: "general",
			visibility: "open",
			memberExternalIDs: [COLLEAGUE],
		});

		expect(addedMembers()).toEqual([COLLEAGUE, BOT_PUBKEY]);
	});

	test("an open channel adds the agent once when the person also chose it", async () => {
		await gatewayOverTheRelay().createChannel(actor, {
			name: "general",
			visibility: "open",
			memberExternalIDs: [BOT_PUBKEY, COLLEAGUE],
		});

		expect(addedMembers()).toEqual([BOT_PUBKEY, COLLEAGUE]);
	});

	test("a private channel holds only the members the person chose", async () => {
		await gatewayOverTheRelay().createChannel(actor, {
			name: "secret",
			visibility: "private",
			memberExternalIDs: [COLLEAGUE],
		});

		expect(addedMembers()).toEqual([COLLEAGUE]);
	});
});
