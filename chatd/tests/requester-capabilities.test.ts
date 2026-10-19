import { afterEach, beforeEach, describe, expect, mock, test } from "bun:test";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_SECRET = "1".repeat(64);
const REQUESTER_SECRET = "2".repeat(64);
const IMPOSTOR_SECRET = "3".repeat(64);
const COLLEAGUE = "e".repeat(64);
const ADMIND_BASE_URL = "http://127.0.0.1:18080";
const SIGNING_SECRET_URL = `${ADMIND_BASE_URL}/admin/api/buzz/signing-secret`;
const REMOVE_USER_KIND = 9001;

type PublishedByKey = { secretHex: string; kind: number; tags: string[][] };

const published: PublishedByKey[] = [];
let roster: Array<[string, string]> = [];

function rosterEvent(): BuzzEvent {
	return {
		id: "roster",
		pubkey: "relay",
		created_at: 1,
		kind: 39002,
		tags: [["d", CHANNEL], ...roster.map(([pubkey, role]) => ["p", pubkey, "", role])],
		content: "",
		sig: "",
	};
}

mock.module("../src/adapters/buzz/relay-client.ts", () => ({
	createBuzzRelayClient: (_relayURL: string, secretHex: string) => ({
		pubkeyHex: pubkeyFromSecret(secretHex),
		connect: async () => {},
		disconnect: () => {},
		subscribe: () => {},
		query: async () => [rosterEvent()],
		queryComplete: async () => ({ events: [rosterEvent()], complete: true }),
		publish: async (kind: number, content: string, tags: string[][]) => {
			published.push({ secretHex, kind, tags });
			return { id: "published", pubkey: pubkeyFromSecret(secretHex), created_at: 300, kind, tags, content, sig: "" };
		},
		publishForAcknowledgement: async () => "",
	}),
}));

const { BuzzAdapter } = await import("../src/adapters/buzz/adapter.ts");
const { pubkeyFromSecret } = await import("../src/adapters/buzz/user-session.ts");
const { createBuzzPersonalGateway } = await import("../src/personal/buzz.ts");
const { createOutboundHandler } = await import("../src/outbound.ts");

const REQUESTER = pubkeyFromSecret(REQUESTER_SECRET);

const realFetch = globalThis.fetch;
let admindStatus = 200;

function stubAdmind() {
	globalThis.fetch = (async (input: Request | string | URL, init?: RequestInit) => {
		const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
		if (!url.startsWith(SIGNING_SECRET_URL)) return await realFetch(input as Request, init);
		if (admindStatus !== 200) return new Response("no member signs with that key", { status: admindStatus });
		return Response.json({ secretHex: REQUESTER_SECRET });
	}) as typeof fetch;
}

function configuration(): ChatdConfiguration {
	return {
		botUserName: "internkim",
		blueclawBaseURL: "http://127.0.0.1:8080",
		blueclawIngressURL: undefined,
		relayInboundURL: undefined,
		admindBaseURL: ADMIND_BASE_URL,
		listenPort: 18090,
		listenHostname: "127.0.0.1",
		mattermost: undefined,
		buzz: { relayURL: "ws://localhost:3000", privateKeyHex: BOT_SECRET, accountLinksPath: undefined, authTagJSON: undefined },
	};
}

function ask(capability: string, body: object): Promise<Response> {
	const adapter = new BuzzAdapter({ relayURL: "ws://localhost:3000", privateKeyHex: BOT_SECRET, botDisplayName: "internkim" });
	(adapter as unknown as { relay: unknown }).relay = { pubkeyHex: pubkeyFromSecret(BOT_SECRET) };
	const gateway = createBuzzPersonalGateway(adapter, { relayURL: "ws://localhost:3000" });
	const handler = createOutboundHandler({ buzz: adapter }, configuration(), { buzz: gateway });
	return handler(
		new Request(`http://chatd/v1/platform/buzz/${capability}`, { method: "POST", body: JSON.stringify(body) }),
	);
}

beforeEach(() => {
	published.length = 0;
	roster = [];
	admindStatus = 200;
	stubAdmind();
});

afterEach(() => {
	globalThis.fetch = realFetch;
});

describe("a person capability asked for the requester", () => {
	test("removes a member under the requester's own key when the requester owns the channel", async () => {
		roster = [[REQUESTER, "owner"], [COLLEAGUE, "member"]];

		const response = await ask("requester.channel.member.remove", {
			requesterPubkeyHex: REQUESTER,
			conversationID: CHANNEL,
			externalID: COLLEAGUE,
		});

		expect(response.status).toBe(200);
		expect(published).toEqual([
			{ secretHex: REQUESTER_SECRET, kind: REMOVE_USER_KIND, tags: [["h", CHANNEL], ["p", COLLEAGUE]] },
		]);
	});

	test("is refused for the same reason the screen is when the requester owns nothing", async () => {
		roster = [[COLLEAGUE, "owner"], [REQUESTER, "member"]];

		const response = await ask("requester.channel.member.remove", {
			requesterPubkeyHex: REQUESTER,
			conversationID: CHANNEL,
			externalID: COLLEAGUE,
		});

		expect(response.status).toBe(409);
		expect(((await response.json()) as { reason: string }).reason).toBe("not-owner");
		expect(published).toEqual([]);
	});

	test("signs as the requester even when the caller hands it another actor", async () => {
		roster = [[REQUESTER, "owner"], [COLLEAGUE, "member"]];

		await ask("requester.channel.delete", {
			requesterPubkeyHex: REQUESTER,
			actor: { kind: "buzz-secret", secret: IMPOSTOR_SECRET },
			conversationID: CHANNEL,
		});

		expect(published.map((event) => event.secretHex)).toEqual([REQUESTER_SECRET]);
	});

	test("refuses a call that names no requester before the relay is touched", async () => {
		const response = await ask("requester.channel.leave", { conversationID: CHANNEL });

		expect(response.status).toBe(400);
		expect(published).toEqual([]);
	});

	test("refuses a requester this device holds no key for", async () => {
		admindStatus = 404;

		const response = await ask("requester.channel.leave", { requesterPubkeyHex: REQUESTER, conversationID: CHANNEL });

		expect(response.status).toBe(401);
		expect(published).toEqual([]);
	});
});
