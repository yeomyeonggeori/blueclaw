import { describe, expect, it } from "bun:test";
import { MattermostAdapter } from "../src/adapters/mattermost/adapter.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import { createOutboundHandler } from "../src/outbound.ts";
import { parseDirectMessagePostRequest } from "../src/outbound-parse.ts";

const AGENT_KEY = "1".repeat(64);
const MEMBER_PUBKEY = "2".repeat(64);

function createConfiguration(): ChatdConfiguration {
	return {
		botUserName: "mattermost-bot",
		blueclawBaseURL: "https://blueclaw.example.com",
		blueclawIngressURL: "https://blueclaw.example.com/ingress",
		admindBaseURL: undefined,
		listenPort: 18090,
		listenHostname: "127.0.0.1",
		mattermost: {
			baseURL: "https://mattermost.example.com",
			botToken: "test-token",
			actionCallbackURL: undefined,
			adminToken: undefined,
		},
		buzz: {
			relayURL: "wss://relay.example.com",
			privateKeyHex: AGENT_KEY,
			accountLinksPath: undefined,
			authTagJSON: undefined,
		},
	};
}

function postRequest(body: unknown): Request {
	return new Request("https://chatd.internal/v1/platform/mattermost/dm.post", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
}

describe("dm.post", () => {
	it("parses a member pubkey and a message", () => {
		const request = parseDirectMessagePostRequest({
			counterpartPubkeyHex: MEMBER_PUBKEY.toUpperCase(),
			message: "안내드립니다",
		});
		expect(request.counterpartPubkeyHex).toBe(MEMBER_PUBKEY);
		expect(request.message).toBe("안내드립니다");
	});

	it("refuses a malformed pubkey", async () => {
		const adapter = new MattermostAdapter({
			baseUrl: "https://mattermost.example.com",
			botToken: "test-token",
			userName: "mattermost-bot",
		});
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());
		const response = await handler(postRequest({ counterpartPubkeyHex: "not-a-key", message: "안내" }));
		expect(response.status).toBe(400);
	});

	it("refuses an empty message", async () => {
		const adapter = new MattermostAdapter({
			baseUrl: "https://mattermost.example.com",
			botToken: "test-token",
			userName: "mattermost-bot",
		});
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());
		const response = await handler(postRequest({ counterpartPubkeyHex: MEMBER_PUBKEY, message: "  " }));
		expect(response.status).toBe(502);
		const body = (await response.json()) as { error: string };
		expect(body.error).toContain("message");
	});

	it("refuses a platform whose adapter cannot sign buzz direct messages", async () => {
		const adapter = new MattermostAdapter({
			baseUrl: "https://mattermost.example.com",
			botToken: "test-token",
			userName: "mattermost-bot",
		});
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());
		const response = await handler(postRequest({ counterpartPubkeyHex: MEMBER_PUBKEY, message: "안내" }));
		expect(response.status).toBe(502);
		const body = (await response.json()) as { error: string };
		expect(body.error).toContain("does not support user direct messages");
	});
});
