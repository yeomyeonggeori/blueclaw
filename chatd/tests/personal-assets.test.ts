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

type Seen = { url: string; authorization: string | null };

function serveMattermost(imageBytes: number): Seen[] {
	const seen: Seen[] = [];
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		const url = String(input);
		seen.push({ url, authorization: new Headers(init?.headers).get("Authorization") });
		if (url.endsWith("/image")) {
			return new Response(new Uint8Array(imageBytes), {
				headers: { "content-type": "image/png" },
			});
		}
		if (url.includes("/emoji/name/")) return Response.json({ id: "emoji-1", name: "shipit" });
		return Response.json([{ id: "emoji-1", name: "shipit" }]);
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

describe("a person's assets are read with that person's own credential", () => {
	test("listing emoji carries the member token and draws nothing", async () => {
		const seen = serveMattermost(1_000);

		const answer = await call("mattermost", "person.emoji.list", { actor });

		expect(await answer.json()).toEqual({ emoji: [{ name: "shipit" }] });
		expect(seen).toHaveLength(1);
		expect(seen[0]?.authorization).toBe("Bearer a-members-own-token");
	});

	test("an emoji drawing resolves the name to an id, then fetches that one image", async () => {
		const seen = serveMattermost(1_000);

		const answer = await call("mattermost", "person.emoji.image", {
			actor,
			name: "shipit",
			largestBytes: 100_000,
		});

		const { image } = (await answer.json()) as { image: { dataURL: string } };
		expect(image.dataURL.startsWith("data:image/png;base64,")).toBe(true);
		expect(seen.map((request) => new URL(request.url).pathname)).toEqual([
			"/api/v4/emoji/name/shipit",
			"/api/v4/emoji/emoji-1/image",
		]);
	});

	test("a drawing past what the caller can carry comes back empty", async () => {
		serveMattermost(120_000);

		const answer = await call("mattermost", "person.picture", {
			actor,
			externalID: "person-1",
			largestBytes: 100_000,
		});

		expect(await answer.json()).toEqual({ image: null });
	});

	test("the caller must say what it can carry, because chatd knows no transport", async () => {
		serveMattermost(1_000);

		const answer = await call("mattermost", "person.picture", { actor, externalID: "person-1" });

		expect(answer.status).toBe(400);
	});

	test("a platform without custom emoji answers with none rather than failing", async () => {
		const answer = await call("buzz", "person.emoji.list", {
			actor: { kind: "buzz-secret", secret: "abc" },
		});

		expect(await answer.json()).toEqual({ emoji: [] });
	});
});
