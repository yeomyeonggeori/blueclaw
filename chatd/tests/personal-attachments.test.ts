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
	test("the file is stored first, and the post names what came back", async () => {
		const seen = serveMattermost(11);

		const response = await call("mattermost", "person.message.send", {
			actor,
			conversationID: "channel-1",
			body: "here it is",
			attachments: [{ filename: "evidence.png", contentType: "image/png", contentBase64: "AAAA" }],
		});

		expect(response.status).toBe(200);
		const upload = seen.find((request) => request.url.endsWith("/api/v4/files"));
		expect((upload?.body as { channelID: string }).channelID).toBe("channel-1");
		const post = seen.find((request) => request.url.endsWith("/posts"));
		expect((post?.body as { file_ids: string[] }).file_ids).toEqual(["file-1"]);
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

	test("what comes back describes the file, without carrying it", async () => {
		serveMattermost(11);

		const response = await call("mattermost", "person.message.send", {
			actor,
			conversationID: "channel-1",
			body: "here it is",
			attachments: [{ filename: "evidence.png", contentType: "image/png", contentBase64: "AAAA" }],
		});

		const message = (await response.json()) as {
			attachments: { id: string; filename: string; contentType: string; sizeBytes: number; digest: string }[];
		};
		expect(message.attachments).toEqual([
			{ id: "file-1", filename: "evidence.png", contentType: "image/png", sizeBytes: 11, digest: "" },
		]);
	});

	test("an image carries the proportions the messenger reported", async () => {
		serveMattermost(11, { width: 1200, height: 1600 });

		const response = await call("mattermost", "person.message.send", {
			actor,
			conversationID: "channel-1",
			body: "here it is",
			attachments: [{ filename: "evidence.png", contentType: "image/png", contentBase64: "AAAA" }],
		});

		const message = (await response.json()) as {
			attachments: { widthPixels?: number; heightPixels?: number }[];
		};
		expect(message.attachments.map((attachment) => [attachment.widthPixels, attachment.heightPixels])).toEqual([
			[1200, 1600],
		]);
	});
});

describe("reading one attachment", () => {
	test("a file inside the limit comes back with its name and type", async () => {
		serveMattermost(11);

		const response = await call("mattermost", "person.message.attachment", {
			actor,
			messageID: "file-1",
			largestBytes: 1_000,
		});

		const answer = (await response.json()) as { file: { filename: string; contentBase64: string } | null };
		expect(answer.file?.filename).toBe("evidence.png");
		expect(Buffer.from(answer.file?.contentBase64 ?? "", "base64")).toHaveLength(11);
	});

	test("a file past the limit is refused before its bytes are read", async () => {
		const seen = serveMattermost(2_000);

		const response = await call("mattermost", "person.message.attachment", {
			actor,
			messageID: "file-1",
			largestBytes: 1_000,
		});

		const answer = (await response.json()) as { file: unknown };
		expect(answer.file).toBeNull();
		expect(seen.some((request) => request.url.endsWith("/files/file-1"))).toBe(false);
	});
});
