import { afterEach, describe, expect, test } from "bun:test";
import { createOutboundHandler } from "../src/outbound.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";

const actor = { kind: "buzz-secret", secret: "1".repeat(64) };
const realFetch = globalThis.fetch;
const handler = createOutboundHandler({ buzz: {} } as never, {} as never, {
	buzz: createBuzzPersonalGateway({} as never, { relayURL: "wss://relay.test" }),
});

function ask(capability: string, body: unknown): Promise<Response> {
	return handler(new Request(`http://127.0.0.1/v1/platform/buzz/${capability}`, { method: "POST", body: JSON.stringify(body) }));
}

afterEach(() => {
	globalThis.fetch = realFetch;
});

describe("a file the messenger's store will not take", () => {
	test("answers 415 with the digest and size, so the caller can keep it elsewhere", async () => {
		const content = new TextEncoder().encode("<html>not wanted</html>");
		globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
			if (String(input).startsWith("https://store.test/")) {
				return new Response(content, {
					status: 206,
					headers: { "content-range": `bytes 0-${content.byteLength - 1}/${content.byteLength}` },
				});
			}
			expect(init?.method).toBe("PUT");
			return new Response("disallowed content type: text/html", { status: 415 });
		}) as typeof fetch;

		const answer = await ask("person.media.upload", { actor, sourceURL: "https://store.test/page.html", contentType: "text/html" });

		expect(answer.status).toBe(415);
		const refused = ((await answer.json()) as { refused: { digest: string; sizeBytes: number; status: number } }).refused;
		expect(refused).toEqual({
			status: 415,
			reason: "disallowed content type: text/html",
			digest: new Bun.CryptoHasher("sha256").update(content).digest("hex"),
			sizeBytes: content.byteLength,
		} as never);
	});
});

describe("reading a file", () => {
	test("asks for one bounded range, never the whole file", async () => {
		const answer = await ask("person.media.read", {
			actor,
			mediaURL: `https://relay.test/media/${"a".repeat(64)}.mp4`,
			range: "bytes=0-",
		});

		expect(answer.status).toBe(400);
	});

	test("a file another store holds is not this messenger's to read", async () => {
		const answer = await ask("person.media.read", {
			actor,
			mediaURL: "https://elsewhere.test/file.bin",
			range: "bytes=0-99",
		});

		expect(answer.status).toBe(400);
	});
});
