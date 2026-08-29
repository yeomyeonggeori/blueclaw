import { beforeEach, describe, expect, mock, test } from "bun:test";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_SECRET = "1".repeat(64);
const REQUESTER = "f".repeat(64);
const STRANGER = "e".repeat(64);
const BOT_MESSAGE = "a".repeat(64);
const REQUESTER_MESSAGE = "b".repeat(64);
const STRANGER_MESSAGE = "c".repeat(64);
const KEY_SEED = "device-identity-seed";
const REQUESTER_EMAIL = "Sample@Example.com";

type Published = { kind: number; content: string; tags: string[][] };
type PublishedByKey = Published & { secretHex: string };

const publishedByAnotherKey: PublishedByKey[] = [];
let relayRefusal: string | undefined;

mock.module("../src/adapters/buzz/relay-client.ts", () => ({
	createBuzzRelayClient: (_relayURL: string, secretHex: string) => ({
		pubkeyHex: publicKeyOf(secretHex),
		connect: async () => {},
		disconnect: () => {},
		subscribe: () => {},
		query: async () => [],
		publish: async (kind: number, content: string, tags: string[][]) => {
			if (relayRefusal) throw new Error(relayRefusal);
			publishedByAnotherKey.push({ secretHex, kind, content, tags });
			return { id: "requester-event", pubkey: publicKeyOf(secretHex), created_at: 300, kind, tags, content, sig: "" };
		},
		publishForAcknowledgement: async () => "",
	}),
}));

const { BuzzAdapter } = await import("../src/adapters/buzz/adapter.ts");
const { deriveBuzzSecret } = await import("../src/adapters/buzz/identity.ts");
const { pubkeyFromSecret } = await import("../src/adapters/buzz/user-session.ts");
const { createOutboundHandler } = await import("../src/outbound.ts");

function publicKeyOf(secretHex: string): string {
	return pubkeyFromSecret(secretHex);
}

function messageEvent(id: string, pubkey: string): BuzzEvent {
	return { id, pubkey, created_at: 100, kind: 9, tags: [["h", CHANNEL]], content: "먼저 쓴 글", sig: "" };
}

function adminListEvent(pubkeys: string[]): BuzzEvent {
	return {
		id: "d".repeat(64),
		pubkey: "0".repeat(64),
		created_at: 200,
		kind: 39001,
		tags: [["d", CHANNEL], ...pubkeys.map((pubkey) => ["p", pubkey, "owner"])],
		content: "",
		sig: "",
	};
}

function withMessages(admins: BuzzEvent[] = []) {
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: BOT_SECRET,
		botDisplayName: "internkim",
	});
	const botPubkey = pubkeyFromSecret(BOT_SECRET);
	const messages = [
		messageEvent(BOT_MESSAGE, botPubkey),
		messageEvent(REQUESTER_MESSAGE, REQUESTER),
		messageEvent(STRANGER_MESSAGE, STRANGER),
	];
	const publishedAsTheBot: Published[] = [];
	(adapter as unknown as { relay: unknown }).relay = {
		pubkeyHex: botPubkey,
		query: async (filter: { ids?: string[]; kinds?: number[] }) => {
			if (filter.ids) return messages.filter((event) => filter.ids?.includes(event.id));
			if (filter.kinds?.includes(39001)) return admins;
			if (filter.kinds?.includes(9)) return messages;
			return [];
		},
		publish: async (kind: number, content: string, tags: string[][]) => {
			publishedAsTheBot.push({ kind, content, tags });
			return { id: "published", pubkey: botPubkey, created_at: 300, kind, tags, content, sig: "" };
		},
	};
	return { adapter, botPubkey, publishedAsTheBot };
}

function configurationHoldingSeed(keySeed: string | undefined): ChatdConfiguration {
	return {
		botUserName: "internkim",
		blueclawBaseURL: "http://127.0.0.1:8080",
		blueclawIngressURL: undefined,
		admindBaseURL: undefined,
		listenPort: 18090,
		listenHostname: "127.0.0.1",
		mattermost: undefined,
		buzz: {
			relayURL: "ws://localhost:3000",
			privateKeyHex: BOT_SECRET,
			accountLinksPath: undefined,
			authTagJSON: undefined,
			keySeed,
		},
	};
}

function editRequest(messageID: string, requesterEmail?: string): Request {
	return new Request("http://chatd/v1/platform/buzz/message.edit", {
		method: "POST",
		body: JSON.stringify({
			replyTargetID: `buzz:${CHANNEL}`,
			messageID,
			message: "고친 글",
			requesterPubkeyHex: REQUESTER,
			requesterEmail,
		}),
	});
}

function deleteRequest(messageID: string, requesterEmail?: string): Request {
	return new Request("http://chatd/v1/platform/buzz/message_delete", {
		method: "POST",
		body: JSON.stringify({
			replyTargetID: `buzz:${CHANNEL}`,
			messageID,
			requesterPubkeyHex: REQUESTER,
			requesterEmail,
		}),
	});
}

beforeEach(() => {
	publishedByAnotherKey.length = 0;
	relayRefusal = undefined;
});

describe("message.edit signs as the person it acts for", () => {
	test("the assistant's own message is still edited by the assistant", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(editRequest(BOT_MESSAGE, REQUESTER_EMAIL));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot.map((event) => event.kind)).toEqual([40003]);
		expect(publishedByAnotherKey).toEqual([]);
	});

	test("somebody else's message is edited under the requester's own derived key", async () => {
		const { adapter, botPubkey, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER_EMAIL));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey.map((event) => event.kind)).toEqual([40003]);
		const signedBy = publishedByAnotherKey[0]?.secretHex ?? "";
		expect(signedBy).toBe(deriveBuzzSecret(KEY_SEED, REQUESTER_EMAIL));
		expect(pubkeyFromSecret(signedBy)).not.toBe(botPubkey);
	});

	test("a third person's message is signed as the requester and left to the relay", async () => {
		const { adapter } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(editRequest(STRANGER_MESSAGE, REQUESTER_EMAIL));

		expect(response.status).toBe(200);
		expect(publishedByAnotherKey[0]?.secretHex).toBe(deriveBuzzSecret(KEY_SEED, REQUESTER_EMAIL));
	});

	test("a refusal from the relay reaches the caller as it was written", async () => {
		const { adapter } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));
		relayRefusal = "relay rejected event: invalid: must be event author to edit";

		const response = await handler(editRequest(STRANGER_MESSAGE, REQUESTER_EMAIL));

		expect(response.status).toBe(502);
		expect(((await response.json()) as { error: string }).error).toContain("must be event author to edit");
	});

	test("a request naming no requester may still change the assistant's message", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		expect((await handler(editRequest(BOT_MESSAGE))).status).toBe(200);
		expect(publishedAsTheBot.map((event) => event.kind)).toEqual([40003]);
	});

	test("a request naming no requester may change nothing else", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(editRequest(REQUESTER_MESSAGE));

		expect(response.status).toBe(403);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
	});

	test("a device holding no seed may change nothing but the assistant's own", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(undefined));

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER_EMAIL));

		expect(response.status).toBe(403);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
	});
});

describe("message_delete signs as the person it acts for", () => {
	test("the requester's own message is deleted under the requester's key", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(deleteRequest(REQUESTER_MESSAGE, REQUESTER_EMAIL));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey.map((event) => event.kind)).toEqual([9005]);
		expect(publishedByAnotherKey[0]?.secretHex).toBe(deriveBuzzSecret(KEY_SEED, REQUESTER_EMAIL));
	});

	test("the assistant's own message is deleted by the assistant", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(deleteRequest(BOT_MESSAGE, REQUESTER_EMAIL));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot.map((event) => event.kind)).toEqual([9005]);
		expect(publishedByAnotherKey).toEqual([]);
	});
});

describe("message.search flags", () => {
	test("names what the requester wrote and what the assistant wrote", async () => {
		const { adapter, botPubkey } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(
			new Request("http://chatd/v1/platform/buzz/message.search", {
				method: "POST",
				body: JSON.stringify({ channelID: CHANNEL, queries: [], requesterPubkeyHex: REQUESTER }),
			}),
		);

		expect(response.status).toBe(200);
		const body = (await response.json()) as {
			candidates: { authorPubkeyHex: string; editable: boolean; deletable: boolean }[];
		};
		const editableByAuthor = new Map(
			body.candidates.map((candidate) => [candidate.authorPubkeyHex, candidate.editable]),
		);
		expect(editableByAuthor.get(botPubkey)).toBe(true);
		expect(editableByAuthor.get(REQUESTER)).toBe(true);
		expect(editableByAuthor.get(STRANGER)).toBe(false);
	});

	test("a channel administrator may delete a third person's message but not edit it", async () => {
		const { adapter } = withMessages([adminListEvent([REQUESTER])]);
		const handler = createOutboundHandler({ buzz: adapter }, configurationHoldingSeed(KEY_SEED));

		const response = await handler(
			new Request("http://chatd/v1/platform/buzz/message.search", {
				method: "POST",
				body: JSON.stringify({ channelID: CHANNEL, queries: [], requesterPubkeyHex: REQUESTER }),
			}),
		);

		const body = (await response.json()) as {
			candidates: { authorPubkeyHex: string; editable: boolean; deletable: boolean }[];
		};
		const stranger = body.candidates.find((candidate) => candidate.authorPubkeyHex === STRANGER);
		expect(stranger?.editable).toBe(false);
		expect(stranger?.deletable).toBe(true);
	});
});
