import { afterEach, describe, expect, it, mock } from "bun:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import { importAttachmentToDirectory, workspaceWritePath } from "../src/outbound-attachments.ts";

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

describe("workspaceWritePath", () => {
	it("maps the workspace vocabulary onto the configured host root", () => {
		const configuration = createConfiguration({ workspaceRootPath: "/root/.blueclaw/workspace" });
		expect(workspaceWritePath(configuration, "/workspace/inbox/buzz/잡담")).toBe(
			"/root/.blueclaw/workspace/inbox/buzz/잡담",
		);
		expect(workspaceWritePath(configuration, "/workspace")).toBe("/root/.blueclaw/workspace");
	});

	it("writes verbatim when no root is configured or the path is not workspace-relative", () => {
		expect(workspaceWritePath(createConfiguration(), "/workspace/inbox")).toBe("/workspace/inbox");
		const configuration = createConfiguration({ workspaceRootPath: "/root/.blueclaw/workspace" });
		expect(workspaceWritePath(configuration, "/tmp/elsewhere")).toBe("/tmp/elsewhere");
	});
});

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

describe("importAttachmentToDirectory", () => {
	it("imports a buzz attachment into the workspace and answers in workspace vocabulary", async () => {
		const hostRoot = await mkdtemp(path.join(tmpdir(), "chatd-workspace-"));
		const configuration = createConfiguration({ workspaceRootPath: hostRoot });
		const adapter = {
			fetchAttachment: async () =>
				new Response("image bytes", { status: 200, headers: { "Content-Type": "image/png" } }),
		};
		try {
			const imported = await importAttachmentToDirectory(adapter, configuration, "/workspace/inbox/buzz/잡담", {
				platform: "buzz",
				url: "http://localhost:3000/media/187958fc.png",
				filename: "지도.png",
			});
			expect(imported.isAvailable).toBe(true);
			expect(imported.path).toBe("/workspace/inbox/buzz/잡담/지도.png");
			expect(imported.contentType).toBe("image/png");
			expect(await readFile(path.join(hostRoot, "inbox/buzz/잡담/지도.png"), "utf8")).toBe("image bytes");
		} finally {
			await rm(hostRoot, { recursive: true, force: true });
		}
	});

	it("reports a failed download without failing the import call", async () => {
		const configuration = createConfiguration();
		const adapter = { fetchAttachment: async () => new Response("gone", { status: 404 }) };
		const imported = await importAttachmentToDirectory(adapter, configuration, "/tmp/unused", {
			platform: "buzz",
			url: "http://localhost:3000/media/gone.png",
		});
		expect(imported.isAvailable).toBe(false);
		expect(imported.errorCode).toBe("download_failed");
	});
});
