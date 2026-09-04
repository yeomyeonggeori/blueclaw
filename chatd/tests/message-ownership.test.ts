import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_SECRET = "1".repeat(64);
const REQUESTER_SECRET = "2".repeat(64);
const IMPOSTOR_SECRET = "3".repeat(64);
const STRANGER = "e".repeat(64);
const BOT_MESSAGE = "a".repeat(64);
const REQUESTER_MESSAGE = "b".repeat(64);
const STRANGER_MESSAGE = "c".repeat(64);
const ADMIND_BASE_URL = "http://127.0.0.1:18080";
const SIGNING_SECRET_URL = `${ADMIND_BASE_URL}/admin/api/buzz/signing-secret`;

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
const { pubkeyFromSecret } = await import("../src/adapters/buzz/user-session.ts");
const { createOutboundHandler } = await import("../src/outbound.ts");

const REQUESTER = pubkeyFromSecret(REQUESTER_SECRET);

function publicKeyOf(secretHex: string): string {
	return pubkeyFromSecret(secretHex);
}

const realFetch = globalThis.fetch;
const signingSecretAsks: string[] = [];
let admindAnswer: { status: number; secretHex?: string } = { status: 200, secretHex: REQUESTER_SECRET };
let admindIsReachable = true;

function stubAdmind() {
	globalThis.fetch = (async (input: Request | string | URL, init?: RequestInit) => {
		const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
		if (!url.startsWith(SIGNING_SECRET_URL)) return await realFetch(input as Request, init);
		signingSecretAsks.push(String(init?.body ?? ""));
		if (!admindIsReachable) throw new Error("connection refused");
		if (admindAnswer.status !== 200) {
			return new Response("no member of this company signs with that key", { status: admindAnswer.status });
		}
		return Response.json({ secretHex: admindAnswer.secretHex });
	}) as typeof fetch;
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

function configurationNaming(admindBaseURL: string | undefined): ChatdConfiguration {
	return {
		botUserName: "internkim",
		blueclawBaseURL: "http://127.0.0.1:8080",
		blueclawIngressURL: undefined,
		relayInboundURL: undefined,
		admindBaseURL,
		listenPort: 18090,
		listenHostname: "127.0.0.1",
		mattermost: undefined,
		buzz: {
			relayURL: "ws://localhost:3000",
			privateKeyHex: BOT_SECRET,
			accountLinksPath: undefined,
			authTagJSON: undefined,
		},
	};
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
		body: JSON.stringify({
			replyTargetID: `buzz:${CHANNEL}`,
			messageID,
			requesterPubkeyHex,
		}),
	});
}

beforeEach(() => {
	publishedByAnotherKey.length = 0;
	signingSecretAsks.length = 0;
	relayRefusal = undefined;
	admindAnswer = { status: 200, secretHex: REQUESTER_SECRET };
	admindIsReachable = true;
	stubAdmind();
});

afterEach(() => {
	globalThis.fetch = realFetch;
});

describe("message.edit signs as the person it acts for", () => {
	test("the assistant's own message is still edited by the assistant", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

		const response = await handler(editRequest(BOT_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot.map((event) => event.kind)).toEqual([40003]);
		expect(publishedByAnotherKey).toEqual([]);
		expect(signingSecretAsks).toEqual([]);
	});

	test("somebody else's message is edited under the key admind names for the requester", async () => {
		const { adapter, botPubkey, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey.map((event) => event.kind)).toEqual([40003]);
		const signedBy = publishedByAnotherKey[0]?.secretHex ?? "";
		expect(pubkeyFromSecret(signedBy)).toBe(REQUESTER);
		expect(pubkeyFromSecret(signedBy)).not.toBe(botPubkey);
		expect(signingSecretAsks).toEqual([JSON.stringify({ pubkeyHex: REQUESTER })]);
	});

	test("a key that does not belong to the requester is refused instead of signed with", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));
		admindAnswer = { status: 200, secretHex: IMPOSTOR_SECRET };

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(403);
		expect(((await response.json()) as { error: string }).error).toContain("is not the key");
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
	});

	test("a refusal never carries the key it refused", async () => {
		const { adapter } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));
		admindAnswer = { status: 200, secretHex: IMPOSTOR_SECRET };

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER));

		const message = ((await response.json()) as { error: string }).error;
		expect(message).not.toContain(IMPOSTOR_SECRET);
		expect(message).not.toContain(REQUESTER_SECRET);
	});

	test("a third person's message is signed as the requester and left to the relay", async () => {
		const { adapter } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

		const response = await handler(editRequest(STRANGER_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(pubkeyFromSecret(publishedByAnotherKey[0]?.secretHex ?? "")).toBe(REQUESTER);
	});

	test("a refusal from the relay reaches the caller as it was written", async () => {
		const { adapter } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));
		relayRefusal = "relay rejected event: invalid: must be event author to edit";

		const response = await handler(editRequest(STRANGER_MESSAGE, REQUESTER));

		expect(response.status).toBe(502);
		expect(((await response.json()) as { error: string }).error).toContain("must be event author to edit");
	});

	test("a request naming no requester may still change the assistant's message", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

		expect((await handler(editRequest(BOT_MESSAGE))).status).toBe(200);
		expect(publishedAsTheBot.map((event) => event.kind)).toEqual([40003]);
	});

	test("a request naming no requester may change nothing else", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

		const response = await handler(editRequest(REQUESTER_MESSAGE));

		expect(response.status).toBe(403);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
		expect(signingSecretAsks).toEqual([]);
	});

	test("a key admind does not know leaves the assistant's own messages the only ones changed", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));
		admindAnswer = { status: 404 };

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(403);
		expect(((await response.json()) as { error: string }).error).toContain("404");
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
		expect((await handler(editRequest(BOT_MESSAGE, REQUESTER))).status).toBe(200);
		expect(publishedAsTheBot.map((event) => event.kind)).toEqual([40003]);
	});

	test("an unreachable admind is said out loud rather than passed over", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));
		admindIsReachable = false;

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(403);
		expect(((await response.json()) as { error: string }).error).toContain("cannot reach");
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
	});

	test("a device naming no admind may change nothing but the assistant's own", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(undefined));

		const response = await handler(editRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(403);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
		expect(signingSecretAsks).toEqual([]);
	});
});

describe("message_delete signs as the person it acts for", () => {
	test("the requester's own message is deleted under the requester's key", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

		const response = await handler(deleteRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey.map((event) => event.kind)).toEqual([9005]);
		expect(pubkeyFromSecret(publishedByAnotherKey[0]?.secretHex ?? "")).toBe(REQUESTER);
	});

	test("the assistant's own message is deleted by the assistant", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

		const response = await handler(deleteRequest(BOT_MESSAGE, REQUESTER));

		expect(response.status).toBe(200);
		expect(publishedAsTheBot.map((event) => event.kind)).toEqual([9005]);
		expect(publishedByAnotherKey).toEqual([]);
	});

	test("a key that does not belong to the requester deletes nothing", async () => {
		const { adapter, publishedAsTheBot } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));
		admindAnswer = { status: 200, secretHex: IMPOSTOR_SECRET };

		const response = await handler(deleteRequest(REQUESTER_MESSAGE, REQUESTER));

		expect(response.status).toBe(403);
		expect(publishedAsTheBot).toEqual([]);
		expect(publishedByAnotherKey).toEqual([]);
	});
});

describe("message.search flags", () => {
	test("names what the requester wrote and what the assistant wrote", async () => {
		const { adapter, botPubkey } = withMessages();
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

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
		const handler = createOutboundHandler({ buzz: adapter }, configurationNaming(ADMIND_BASE_URL));

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
