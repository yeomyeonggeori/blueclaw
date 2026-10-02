import { afterEach, describe, expect, test } from "bun:test";
import { createOutboundHandler } from "../src/outbound.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";
import { createMattermostPersonalGateway } from "../src/personal/mattermost.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const baseURL = "https://mattermost.test";
const configuration = {} as ChatdConfiguration;
const adapters = { mattermost: {}, buzz: {} } as never;
const gateways = {
	mattermost: createMattermostPersonalGateway(baseURL),
	buzz: createBuzzPersonalGateway({} as never, {} as never),
};
const actor = { kind: "mattermost-token", secret: "a-members-own-token" };
const realFetch = globalThis.fetch;

type Seen = { url: string; method: string; body: unknown };

function serveMattermost(fileBytes: number, shape?: { width: number; height: number }): Seen[] {
	const seen: Seen[] = [];
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		const url = String(input);
		const method = init?.method ?? "GET";
		if (url.endsWith("/api/v4/files") && method === "POST") {
			const form = init?.body as FormData;
			seen.push({ url, method, body: { channelID: form.get("channel_id"), file: form.get("files") } });
			return Response.json({ file_infos: [{ id: "file-1" }] });
		}
		if (url.endsWith("/files/file-1/info")) {
			return Response.json({ id: "file-1", name: "evidence.png", mime_type: "image/png", size: fileBytes, ...shape });
		}
		if (url.endsWith("/files/file-1")) {
			return new Response(new Uint8Array(fileBytes), { headers: { "content-type": "image/png" } });
		}
		seen.push({ url, method, body: JSON.parse(String(init?.body ?? "{}")) });
		return Response.json({
			id: "post-1",
			channel_id: "channel-1",
			root_id: "",
			user_id: "person-1",
			message: "here it is",
			create_at: 0,
			edit_at: 0,
			metadata: {
				files: [{ id: "file-1", name: "evidence.png", mime_type: "image/png", size: fileBytes, ...shape }],
			},
		});
	}) as typeof fetch;
	return seen;
}

function call(platform: string, capability: string, body: unknown): Promise<Response> {
	const handler = createOutboundHandler(adapters, configuration, gateways);
	return handler(
		new Request(`http://127.0.0.1/v1/platform/${platform}/${capability}`, {
			method: "POST",
			body: JSON.stringify(body),
		}),
	);
}

afterEach(() => {
	globalThis.fetch = realFetch;
});

describe("sending a message that carries a file", () => {
	test("mattermost refuses a file kept outside its own store, and posts nothing", async () => {
		const seen = serveMattermost(11);

		const response = await call("mattermost", "person.message.send", {
			actor,
			conversationID: "channel-1",
			body: "here it is",
			attachments: [
				{
					filename: "evidence.png",
					contentType: "image/png",
					address: "https://company.supabase.co/storage/v1/object/asset/c/shared/attachment/9f2c.png",
					sizeBytes: 11,
					digest: "9f2c",
				},
			],
		});

		expect(response.status).toBe(501);
		expect(seen.some((request) => request.url.endsWith("/posts"))).toBe(false);
	});

	test("a message carrying none uploads nothing", async () => {
		const seen = serveMattermost(11);

		await call("mattermost", "person.message.send", {
			actor,
			conversationID: "channel-1",
			body: "just words",
		});

		expect(seen.some((request) => request.url.endsWith("/api/v4/files"))).toBe(false);
	});

	test("a file carried inside the call is no longer taken", async () => {
		serveMattermost(11);

		const response = await call("buzz", "person.message.send", {
			actor: { kind: "buzz-secret", secret: "1".repeat(64) },
			conversationID: "channel-1",
			body: "here it is",
			attachments: [{ filename: "evidence.png", contentType: "image/png", contentBase64: "AAAA" }],
		});

		expect(response.status).toBe(400);
		expect(((await response.json()) as { error: string }).error).toBe("missing required field address");
	});
});
