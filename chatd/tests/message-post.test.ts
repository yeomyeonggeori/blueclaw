import { afterEach, describe, expect, it, mock } from "bun:test";
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
