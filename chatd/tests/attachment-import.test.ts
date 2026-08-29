import { afterEach, describe, expect, it, mock } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import { fetchAttachmentForDirectory } from "../src/outbound-attachments.ts";

const AGENT_SECRET = "1".repeat(64);
const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
});

function createConfiguration(overrides: Partial<ChatdConfiguration> = {}): ChatdConfiguration {
	return {
		botUserName: "internkim",
		blueclawBaseURL: "https://blueclaw.example.com",
		blueclawIngressURL: undefined,
		admindBaseURL: undefined,
		listenPort: 18090,
		listenHostname: "127.0.0.1",
		mattermost: undefined,
		buzz: {
			relayURL: "ws://localhost:3000",
			privateKeyHex: AGENT_SECRET,
			accountLinksPath: undefined,
			authTagJSON: undefined,
			keySeed: undefined,
		},
		...overrides,
	};
}

function createBuzzAdapter(): BuzzAdapter {
	return new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: AGENT_SECRET,
		botDisplayName: "internkim",
	});
}

describe("buzz attachment fetch", () => {
	it("refuses a url another host serves", async () => {
		const adapter = createBuzzAdapter();
		const response = await adapter.fetchAttachment({ url: "http://attacker.example.test/media/a.png" });
		expect(response.status).toBe(400);
	});

	it("fetches a relay-served url", async () => {
		const adapter = createBuzzAdapter();
		globalThis.fetch = mock(async () => new Response("png bytes", { status: 200 })) as never;
		const response = await adapter.fetchAttachment({ url: "http://localhost:3000/media/abc.png" });
		expect(response.status).toBe(200);
		expect(await response.text()).toBe("png bytes");
	});
});

describe("fetchAttachmentForDirectory", () => {
	// chatd cannot reach the workspace the agent reads: it hands over the bytes
	// with the path they belong at, and whoever owns that workspace writes them.
	it("answers with the file's bytes and the path they belong at", async () => {
		const adapter = {
			fetchAttachment: async () =>
				new Response("image bytes", { status: 200, headers: { "Content-Type": "image/png" } }),
		};

		const fetched = await fetchAttachmentForDirectory(adapter, "/workspace/inbox/buzz/\uc7a1\ub2f4", {
			platform: "buzz",
			url: "http://localhost:3000/media/187958fc.png",
			filename: "\uc9c0\ub3c4.png",
		});

		expect(fetched.isAvailable).toBe(true);
		expect(fetched.path).toBe("/workspace/inbox/buzz/\uc7a1\ub2f4/\uc9c0\ub3c4.png");
		expect(fetched.contentType).toBe("image/png");
		expect(Buffer.from(fetched.contentBase64 ?? "", "base64").toString("utf8")).toBe("image bytes");
	});

	it("writes nothing of its own", async () => {
		const adapter = {
			fetchAttachment: async () => new Response("image bytes", { status: 200 }),
		};

		const fetched = await fetchAttachmentForDirectory(adapter, "/workspace/inbox/buzz/dm", {
			platform: "buzz",
			url: "http://localhost:3000/media/187958fc.png",
			filename: "map.png",
		});

		expect(await Bun.file(fetched.path ?? "").exists()).toBe(false);
	});

	it("reports a failed download without failing the import call", async () => {
		const adapter = { fetchAttachment: async () => new Response("gone", { status: 404 }) };

		const fetched = await fetchAttachmentForDirectory(adapter, "/workspace/inbox/buzz/dm", {
			platform: "buzz",
			url: "http://localhost:3000/media/gone.png",
		});

		expect(fetched.isAvailable).toBe(false);
		expect(fetched.errorCode).toBe("download_failed");
	});
});

describe("attachmentFilename via fetch", () => {
	// The markdown alt text names buzz attachments "image", with no extension;
	// the url ends in the blob's own hash and type. An inbox of files named
	// image, image-2 answers nobody days later.
	it("prefers the url's own segment over an extensionless alt text", async () => {
		const adapter = {
			fetchAttachment: async () => new Response("bytes", { status: 200, headers: { "Content-Type": "image/png" } }),
		};

		const fetched = await fetchAttachmentForDirectory(adapter, "/workspace/inbox/buzz/dm", {
			platform: "buzz",
			url: "http://localhost:3000/media/187958fc.png",
			filename: "image",
		});

		expect(fetched.filename).toBe("187958fc.png");
		expect(fetched.path).toBe("/workspace/inbox/buzz/dm/187958fc.png");
	});

	it("keeps a real filename the sender chose", async () => {
		const adapter = {
			fetchAttachment: async () => new Response("bytes", { status: 200 }),
		};

		const fetched = await fetchAttachmentForDirectory(adapter, "/workspace/inbox/buzz/dm", {
			platform: "buzz",
			url: "http://localhost:3000/media/187958fc.png",
			filename: "지도.png",
		});

		expect(fetched.filename).toBe("지도.png");
	});
});
