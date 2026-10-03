import { afterEach, describe, expect, it, mock } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import { MattermostAdapter } from "../src/adapters/mattermost/adapter.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import { createOutboundHandler } from "../src/outbound.ts";

const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
});

function createAdapter(): MattermostAdapter {
	const adapter = new MattermostAdapter({
		baseUrl: "https://mattermost.example.com",
		botToken: "test-token",
		userName: "mattermost-bot",
	});
	(adapter as unknown as { botUserId: string }).botUserId = "bot-user";
	return adapter;
}

function createConfiguration(): ChatdConfiguration {
	return {
		botUserName: "mattermost-bot",
		blueclawBaseURL: "https://blueclaw.example.com",
		blueclawIngressURL: "https://blueclaw.example.com/ingress",
		relayInboundURL: undefined,
		admindBaseURL: undefined,
		listenPort: 18090,
		listenHostname: "127.0.0.1",
		mattermost: {
			baseURL: "https://mattermost.example.com",
			botToken: "test-token",
			actionCallbackURL: undefined,
			adminToken: undefined,
		},
		buzz: undefined,
	};
}

function postRequest(body: unknown): Request {
	return new Request("https://chatd.internal/v1/platform/mattermost/message.post", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
}

function createdPostResponse(): Response {
	return new Response(
		JSON.stringify({ id: "post-9", channel_id: "channel-1", user_id: "bot-user", message: "hi", create_at: 1 }),
		{ status: 201, headers: { "Content-Type": "application/json" } },
	);
}

describe("message.post", () => {
	it("posts to a channel by id", async () => {
		const requests: string[] = [];
		globalThis.fetch = mock(async (input: string | URL | Request) => {
			requests.push(String(input instanceof Request ? input.url : input));
			return createdPostResponse();
		}) as never;
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());

		const response = await handler(postRequest({ channelID: "channel-1", message: "안내드립니다" }));

		expect(response.status).toBe(200);
		const body = (await response.json()) as { messageID: string; channelID: string };
		expect(body.messageID).toBe("post-9");
		expect(body.channelID).toBe("channel-1");
		expect(requests.some((url) => url.endsWith("/api/v4/posts"))).toBe(true);
	});

	it("rejects a channel name on a platform without name lookup", async () => {
		globalThis.fetch = mock(async () => createdPostResponse()) as never;
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());

		const response = await handler(postRequest({ channelName: "잡담", message: "hello" }));

		expect(response.status).toBe(400);
	});

	it("requires a target", async () => {
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());
		const response = await handler(postRequest({ message: "no target" }));
		expect(response.status).toBeGreaterThanOrEqual(400);
	});
});

type Published = { kind: number; content: string; tags: string[][] };

function buzzAdapterRecordingWhatItPublishes(): { adapter: BuzzAdapter; published: Published[] } {
	const adapter = new BuzzAdapter({ relayURL: "ws://localhost:3000", privateKeyHex: "1".repeat(64), botDisplayName: "internkim" });
	const published: Published[] = [];
	(adapter as unknown as { relay: object }).relay = {
		publish: async (kind: number, content: string, tags: string[][]) => {
			published.push({ kind, content, tags });
			return { id: "posted-file-1", kind, content, tags, pubkey: "agent", created_at: 0, sig: "" } as BuzzEvent;
		},
		query: async () => [],
	};
	return { adapter, published };
}

function buzzPostRequest(body: unknown): Request {
	return new Request("https://chatd.internal/v1/platform/buzz/message.post", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
}

const keptDeck = {
	filename: "분기 보고.pdf",
	contentType: "application/pdf",
	address: "http://localhost:3000/media/9f2c.pdf",
	sizeBytes: 2048,
	digest: "9f2c",
};

describe("message.post with a file the messenger already keeps", () => {
	it("posts it in the thread, linked and named, without uploading it again", async () => {
		const uploads: string[] = [];
		globalThis.fetch = mock(async (input: string | URL | Request) => {
			uploads.push(String(input instanceof Request ? input.url : input));
			return new Response("unexpected", { status: 599 });
		}) as never;
		const { adapter, published } = buzzAdapterRecordingWhatItPublishes();
		const handler = createOutboundHandler({ buzz: adapter }, createConfiguration());

		const response = await handler(buzzPostRequest({ threadID: "buzz:channel-1", message: "", attachments: [keptDeck] }));

		expect(response.status).toBe(200);
		expect(((await response.json()) as { messageID: string }).messageID).toBe("posted-file-1");
		expect(uploads).toEqual([]);
		expect(published).toHaveLength(1);
		expect(published[0]?.content).toBe("[분기 보고.pdf](http://localhost:3000/media/9f2c.pdf)");
		expect(published[0]?.tags).toContainEqual([
			"imeta",
			"url http://localhost:3000/media/9f2c.pdf",
			"m application/pdf",
			"x 9f2c",
			"size 2048",
			"filename 분기 보고.pdf",
		]);
	});

	it("is refused on a platform that cannot link a kept file, rather than posted without it", async () => {
		globalThis.fetch = mock(async () => createdPostResponse()) as never;
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());

		const response = await handler(postRequest({ channelID: "channel-1", message: "", attachments: [keptDeck] }));

		expect(response.status).toBeGreaterThanOrEqual(400);
	});
});
