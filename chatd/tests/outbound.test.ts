import { afterEach, describe, expect, it, mock } from "bun:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { MattermostAdapter } from "../src/adapters/mattermost/adapter.ts";
import type { MattermostPost, MattermostUser } from "../src/adapters/mattermost/types.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import { createOutboundHandler } from "../src/outbound.ts";

function adapterInternals<TInternals>(adapter: MattermostAdapter): TInternals {
	return adapter as unknown as TInternals;
}

function createAdapter(withCallback = false): MattermostAdapter {
	const adapter = new MattermostAdapter({
		baseUrl: "https://mattermost.example.com",
		botToken: "test-token",
		userName: "mattermost-bot",
		callbackUrl: withCallback ? "https://bot.example.com/webhooks/mattermost" : undefined,
	});
	adapterInternals<{ botUserId: string }>(adapter).botUserId = "bot-user";
	return adapter;
}

function createConfiguration(overrides: Partial<ChatdConfiguration> = {}): ChatdConfiguration {
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
		buzz: undefined,
		...overrides,
	};
}

function createPost(overrides: Partial<MattermostPost> = {}): MattermostPost {
	return {
		id: "post-1",
		channel_id: "channel-1",
		user_id: "user-1",
		message: "hello world",
		type: "",
		create_at: 1,
		update_at: 1,
		edit_at: 0,
		delete_at: 0,
		is_pinned: false,
		...overrides,
	};
}

function createUser(overrides: Partial<MattermostUser> = {}): MattermostUser {
	return {
		id: "user-1",
		username: "alice",
		...overrides,
	};
}

function jsonResponse(statusCode: number, body: unknown): Response {
	return new Response(JSON.stringify(body), {
		status: statusCode,
		headers: { "Content-Type": "application/json" },
	});
}

function outboundRequest(capabilityName: string, body: unknown): Request {
	return new Request(`https://chatd.internal/v1/platform/mattermost/${capabilityName}`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
}

const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
	mock.restore();
});

describe("createOutboundHandler routing", () => {
	it("rejects non-POST requests", async () => {
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());
		const response = await handler(
			new Request("https://chatd.internal/v1/platform/mattermost/reply.send", { method: "GET" }),
		);
		expect(response.status).toBe(405);
	});

	it("404s for an unknown platform", async () => {
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());
		const notMattermost = new Request("https://chatd.internal/v1/platform/slack/reply.send", {
			method: "POST",
			body: "{}",
		});
		const response = await handler(notMattermost);
		expect(response.status).toBe(404);
	});

	it("404s for an unknown capability", async () => {
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());
		const response = await handler(outboundRequest("not.a.capability", {}));
		expect(response.status).toBe(404);
	});

	it("returns 502 with a message when the request body fails validation", async () => {
		const handler = createOutboundHandler({ mattermost: createAdapter() }, createConfiguration());
		const response = await handler(outboundRequest("reply.send", { message: "hello" }));
		expect(response.status).toBe(502);
		const body = (await response.json()) as { error: string };
		expect(body.error).toContain("replyTargetID");
	});
});

describe("reply.send", () => {
	it("posts a plain text reply and returns the dispatch id", async () => {
		const adapter = createAdapter();
		globalThis.fetch = mock(async () =>
			jsonResponse(201, createPost({ id: "new-post-1", channel_id: "channel-1" })),
		) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });
		const response = await handler(
			outboundRequest("reply.send", { replyTargetID: threadId, message: "hello there" }),
		);

		expect(response.status).toBe(200);
		const body = (await response.json()) as { dispatchID: string };
		expect(body.dispatchID).toBe("new-post-1");
	});

	it("writes the options into the message so they can be answered in words", async () => {
		const adapter = createAdapter(true);
		let requestBody: Record<string, unknown> = {};
		globalThis.fetch = mock(async (_input: unknown, init?: RequestInit) => {
			requestBody = JSON.parse(init?.body as string);
			return jsonResponse(201, createPost({ id: "new-post-2", channel_id: "channel-1" }));
		}) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });
		await handler(
			outboundRequest("reply.send", {
				replyTargetID: threadId,
				message: "please choose",
				interaction: {
					question: "Approve deploy?",
					options: [
						{ key: "approve", label: "Approve" },
						{ key: "deny", label: "Deny" },
					],
				},
			}),
		);

		expect(requestBody.props).toBeUndefined();
		expect(requestBody.message).toBe("please choose\n\nApprove deploy?\n\n1. Approve\n2. Deny");
	});

	it("does not repeat a question the message already asks", async () => {
		const adapter = createAdapter(true);
		let requestBody: Record<string, unknown> = {};
		globalThis.fetch = mock(async (_input: unknown, init?: RequestInit) => {
			requestBody = JSON.parse(init?.body as string);
			return jsonResponse(201, createPost({ id: "new-post-3", channel_id: "channel-1" }));
		}) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });
		await handler(
			outboundRequest("reply.send", {
				replyTargetID: threadId,
				message: "Approve deploy?",
				interaction: {
					question: "Approve deploy?",
					options: [
						{ key: "approve", label: "Approve" },
						{ key: "deny", label: "Deny" },
					],
				},
			}),
		);

		expect(requestBody.message).toBe("Approve deploy?\n\n1. Approve\n2. Deny");
	});

	it("uploads a base64 attachment as a file before posting", async () => {
		const adapter = createAdapter();
		const fetchCalls: string[] = [];
		globalThis.fetch = mock(async (input: unknown) => {
			const url = String(input);
			fetchCalls.push(url);
			if (url.endsWith("/files")) {
				return jsonResponse(201, { file_infos: [{ id: "file-1" }] });
			}
			return jsonResponse(201, createPost({ id: "new-post-3", channel_id: "channel-1" }));
		}) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });
		const response = await handler(
			outboundRequest("reply.send", {
				replyTargetID: threadId,
				message: "here is a file",
				attachments: [{ filename: "note.txt", contentBase64: Buffer.from("hi").toString("base64") }],
			}),
		);

		expect(response.status).toBe(200);
		expect(fetchCalls.some((url) => url.endsWith("/files"))).toBe(true);
	});
});

describe("progress.start and progress.stop", () => {
	it("starts typing against the reply target", async () => {
		const adapter = createAdapter();
		let requestedUrl = "";
		globalThis.fetch = mock(async (input: unknown) => {
			requestedUrl = String(input);
			return jsonResponse(200, {});
		}) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });
		const response = await handler(outboundRequest("progress.start", { replyTargetID: threadId }));

		expect(response.status).toBe(200);
		expect(requestedUrl).toContain("/users/bot-user/typing");
	});

	it("no-ops on progress.stop since Mattermost typing has no stop call", async () => {
		const adapter = createAdapter();
		const fetchMock = mock(async () => jsonResponse(200, {}));
		globalThis.fetch = fetchMock as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const response = await handler(outboundRequest("progress.stop", { replyTargetID: "irrelevant" }));

		expect(response.status).toBe(200);
		expect(fetchMock).not.toHaveBeenCalled();
	});
});

describe("reaction.add and reaction.remove", () => {
	it("adds a reaction by message id", async () => {
		const adapter = createAdapter();
		let requestBody: Record<string, unknown> = {};
		globalThis.fetch = mock(async (_input: unknown, init?: RequestInit) => {
			requestBody = JSON.parse(init?.body as string);
			return jsonResponse(200, {});
		}) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const response = await handler(
			outboundRequest("reaction.add", { messageID: "post-1", emojiName: "white_check_mark" }),
		);

		expect(response.status).toBe(200);
		expect(requestBody.post_id).toBe("post-1");
		expect(requestBody.emoji_name).toBe("white_check_mark");
	});

	it("removes a reaction by message id", async () => {
		const adapter = createAdapter();
		let requestedUrl = "";
		globalThis.fetch = mock(async (input: unknown) => {
			requestedUrl = String(input);
			return jsonResponse(204, {});
		}) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const response = await handler(
			outboundRequest("reaction.remove", { messageID: "post-1", emojiName: "white_check_mark" }),
		);

		expect(response.status).toBe(200);
		expect(requestedUrl).toContain("/posts/post-1/reactions/white_check_mark");
	});
});

describe("history.fetch", () => {
	it("fetches thread history from a raw thread id cursor", async () => {
		const adapter = createAdapter();
		globalThis.fetch = mock(async (input: unknown) => {
			const url = String(input);
			if (url.includes("/thread")) {
				return jsonResponse(200, {
					order: ["post-1"],
					posts: { "post-1": createPost({ id: "post-1", message: "older message" }) },
				});
			}
			return jsonResponse(200, { id: "channel-1", name: "channel-1", type: "O" });
		}) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const threadId = adapter.encodeThreadId({ channelId: "channel-1", rootPostId: "root-1" });
		const response = await handler(outboundRequest("history.fetch", { historyCursor: threadId, limit: 10 }));

		expect(response.status).toBe(200);
		const body = (await response.json()) as { messages: Array<{ text: string }>; conversationType: string };
		expect(body.messages).toHaveLength(1);
		expect(body.messages[0]?.text).toBe("older message");
		expect(body.conversationType).toBe("channel");
	});
});

describe("attachments.import", () => {
	// This process cannot reach the workspace the agent reads, so it answers with
	// the bytes and the path they belong at rather than a file it wrote itself.
	it("answers with the attachment bytes and the path they belong at", async () => {
		const adapter = createAdapter();
		globalThis.fetch = mock(async () => new Response("file contents", { status: 200 })) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const response = await handler(
			outboundRequest("attachments.import", {
				messageID: "post-1",
				targetDirectoryPath: "/workspace/private/people/person-1/inbox/mattermost/post-1",
				inputAttachments: [{ platform: "mattermost", fileID: "file-1", filename: "report.txt" }],
			}),
		);

		expect(response.status).toBe(200);
		const body = (await response.json()) as {
			inputAttachments: Array<{ isAvailable: boolean; path: string; contentBase64: string }>;
			inputParts: Array<{ type: string }>;
		};
		expect(body.inputAttachments[0]?.isAvailable).toBe(true);
		expect(body.inputAttachments[0]?.path).toBe(
			"/workspace/private/people/person-1/inbox/mattermost/post-1/report.txt",
		);
		expect(Buffer.from(body.inputAttachments[0]?.contentBase64 ?? "", "base64").toString("utf8")).toBe(
			"file contents",
		);
		expect(await Bun.file(body.inputAttachments[0]?.path ?? "").exists()).toBe(false);
		expect(body.inputParts).toHaveLength(1);
		expect(body.inputParts[0]?.type).toBe("file");
	});

	it("reports unavailable attachments without a file reference instead of failing the whole import", async () => {
		const adapter = createAdapter();
		globalThis.fetch = mock(async () => new Response("unused", { status: 200 })) as never;
		const targetDirectoryPath = await mkdtemp(path.join(tmpdir(), "chatd-outbound-"));
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		try {
			const response = await handler(
				outboundRequest("attachments.import", {
					messageID: "post-1",
					targetDirectoryPath,
					inputAttachments: [{ platform: "mattermost" }],
				}),
			);

			const body = (await response.json()) as {
				inputAttachments: Array<{ isAvailable: boolean; errorCode: string }>;
			};
			expect(body.inputAttachments[0]?.isAvailable).toBe(false);
			expect(body.inputAttachments[0]?.errorCode).toBe("missing_file_reference");
		} finally {
			await rm(targetDirectoryPath, { recursive: true, force: true });
		}
	});
});

describe("identity.resolve", () => {
	it("resolves the sender's display name", async () => {
		const adapter = createAdapter();
		globalThis.fetch = mock(async () =>
			jsonResponse(200, createUser({ id: "user-1", username: "alice", first_name: "Alice" })),
		) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const response = await handler(outboundRequest("identity.resolve", { senderID: "user-1" }));

		expect(response.status).toBe(200);
		const body = (await response.json()) as { displayName: string };
		expect(body.displayName).toBe("Alice");
	});

	it("returns an empty document when the user cannot be found", async () => {
		const adapter = createAdapter();
		globalThis.fetch = mock(async () => jsonResponse(404, { message: "missing" })) as never;
		const handler = createOutboundHandler({ mattermost: adapter }, createConfiguration());

		const response = await handler(outboundRequest("identity.resolve", { senderID: "user-1" }));

		expect(response.status).toBe(200);
		const body = (await response.json()) as Record<string, unknown>;
		expect(body.displayName).toBeUndefined();
	});
});
