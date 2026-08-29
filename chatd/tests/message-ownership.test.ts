import { describe, expect, test } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import { mayChangeMessage } from "../src/message-ownership.ts";
import { createOutboundHandler } from "../src/outbound.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_SECRET = "1".repeat(64);
const REQUESTER = "f".repeat(64);
const STRANGER = "e".repeat(64);
const BOT_MESSAGE = "a".repeat(64);
const REQUESTER_MESSAGE = "b".repeat(64);
const STRANGER_MESSAGE = "c".repeat(64);

type Published = { kind: number; content: string; tags: string[][] };

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
	const botPubkey = adapter.botPubkey;
	const messages = [
		messageEvent(BOT_MESSAGE, botPubkey),
		messageEvent(REQUESTER_MESSAGE, REQUESTER),
		messageEvent(STRANGER_MESSAGE, STRANGER),
	];
	const published: Published[] = [];
	(adapter as unknown as { relay: unknown }).relay = {
		pubkeyHex: botPubkey,
		query: async (filter: { ids?: string[]; kinds?: number[] }) => {
			if (filter.ids) return messages.filter((event) => filter.ids?.includes(event.id));
			if (filter.kinds?.includes(39001)) return admins;
			if (filter.kinds?.includes(9)) return messages;
			return [];
		},
		publish: async (kind: number, content: string, tags: string[][]) => {
			published.push({ kind, content, tags });
			return { id: "published", pubkey: botPubkey, created_at: 300, kind, tags, content, sig: "" };
		},
	};
	return { adapter, botPubkey, published };
}

function editRequest(messageID: string, requesterPubkeyHex?: string): Request {
	return new Request("http://chatd/v1/platform/buzz/message.edit", {
		method: "POST",
		body: JSON.stringify({
			replyTargetID: `buzz:${CHANNEL}`,
			messageID,
			message: "고친 글",
			requesterPubkeyHex,
		}),
	});
}

function deleteRequest(messageID: string, requesterPubkeyHex?: string): Request {
	return new Request("http://chatd/v1/platform/buzz/message_delete", {
		method: "POST",
		body: JSON.stringify({ replyTargetID: `buzz:${CHANNEL}`, messageID, requesterPubkeyHex }),
	});
}

describe("mayChangeMessage", () => {
	test("the assistant's own message is always changeable", () => {
		expect(mayChangeMessage("bot", "actor", false, "bot")).toBe(true);
	});

	test("a person may change what they wrote", () => {
		expect(mayChangeMessage("actor", "actor", false, "bot")).toBe(true);
	});

	test("a channel administrator may change anyone's", () => {
		expect(mayChangeMessage("stranger", "actor", true, "bot")).toBe(true);
	});

	test("nobody else may change a third person's message", () => {
		expect(mayChangeMessage("stranger", "actor", false, "bot")).toBe(false);
	});
});

describe("message.edit through the agent", () => {
	test("edits the assistant's own message", async () => {
		const { adapter, published } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(editRequest(BOT_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(published[0]?.kind).toBe(40003);
	});

	test("edits the requester's own message", async () => {
		const { adapter, published } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(published[0]?.kind).toBe(40003);
	});

	test("refuses a third person's message and says the matrix", async () => {
		const { adapter, published } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(editRequest(STRANGER_MESSAGE, REQUESTER));

		expect(response.status).toBe(403);
		expect(((await response.json()) as { error: string }).error).toContain("channel admin role");
		expect(published).toEqual([]);
	});

	test("lets a channel administrator edit a third person's message", async () => {
		const { adapter, published } = withMessages([adminListEvent([REQUESTER])]);
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(editRequest(STRANGER_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(published[0]?.kind).toBe(40003);
	});

	test("a request naming no requester may still change the assistant's message", async () => {
		const { adapter } = withMessages([adminListEvent([REQUESTER])]);
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		expect((await handler(editRequest(BOT_MESSAGE))).status).toBe(200);
	});

	test("a request naming no requester may change nothing else", async () => {
		const { adapter, published } = withMessages([adminListEvent([REQUESTER])]);
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(editRequest(REQUESTER_MESSAGE));

		expect(response.status).toBe(403);
		expect(published).toEqual([]);
	});
});

describe("message_delete through the agent", () => {
	test("deletes the requester's own message", async () => {
		const { adapter, published } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(deleteRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(published[0]?.kind).toBe(9005);
	});

	test("refuses a third person's message", async () => {
		const { adapter, published } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(deleteRequest(STRANGER_MESSAGE, REQUESTER));

		expect(response.status).toBe(403);
		expect(published).toEqual([]);
	});
});

describe("message.search flags", () => {
	test("names what the requester may change and what they may not", async () => {
		const { adapter, botPubkey } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(
			new Request("http://chatd/v1/platform/buzz/message.search", {
				method: "POST",
				body: JSON.stringify({ channelID: CHANNEL, queries: [], requesterPubkeyHex: REQUESTER }),
			}),
		);

		expect(response.status).toBe(200);
		const body = (await response.json()) as {
			candidates: { messageID: string; authorPubkeyHex: string; editable: boolean; deletable: boolean }[];
		};
		const changeableByAuthor = new Map(
			body.candidates.map((candidate) => [candidate.authorPubkeyHex, candidate.editable && candidate.deletable]),
		);
		expect(changeableByAuthor.get(botPubkey)).toBe(true);
		expect(changeableByAuthor.get(REQUESTER)).toBe(true);
		expect(changeableByAuthor.get(STRANGER)).toBe(false);
	});

	test("a channel administrator may change every candidate", async () => {
		const { adapter } = withMessages([adminListEvent([REQUESTER])]);
		const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);

		const response = await handler(
			new Request("http://chatd/v1/platform/buzz/message.search", {
				method: "POST",
				body: JSON.stringify({ channelID: CHANNEL, queries: [], requesterPubkeyHex: REQUESTER }),
			}),
		);

		const body = (await response.json()) as { candidates: { editable: boolean; deletable: boolean }[] };
		expect(body.candidates).toHaveLength(3);
		expect(body.candidates.every((candidate) => candidate.editable && candidate.deletable)).toBe(true);
	});
});
