import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";
import { uploadBlob } from "../src/adapters/buzz/blossom.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";

const actor = { kind: "buzz-secret", secret: "1".repeat(64) };
const realFetch = globalThis.fetch;

type ReceivedUpload = {
	contentLength: string | null;
	transferEncoding: string | null;
	declaredDigest: string | null;
	digest: string;
	sizeBytes: number;
};

const received: ReceivedUpload[] = [];
const uploadBodies: unknown[] = [];
let server: ReturnType<typeof Bun.serve>;
let served: Uint8Array = new Uint8Array();

function patterned(sizeBytes: number, seed: number): Uint8Array {
	const bytes = new Uint8Array(sizeBytes);
	for (let index = 0; index < sizeBytes; index += 1) bytes[index] = (index * 13 + seed) & 0xff;
	return bytes;
}

function digestOf(bytes: Uint8Array): string {
	return new Bun.CryptoHasher("sha256").update(bytes).digest("hex");
}

async function receivedUpload(request: Request): Promise<Response> {
	const bytes = new Uint8Array(await request.arrayBuffer());
	const digest = digestOf(bytes);
	received.push({
		contentLength: request.headers.get("content-length"),
		transferEncoding: request.headers.get("transfer-encoding"),
		declaredDigest: request.headers.get("x-sha-256"),
		digest,
		sizeBytes: bytes.byteLength,
	});
	return Response.json({ url: `http://127.0.0.1:${server.port}/${digest}`, sha256: digest, size: bytes.byteLength, type: "application/pdf" });
}

function servedRange(request: Request): Response {
	const asked = /^bytes=(\d+)-(\d+)$/.exec(request.headers.get("range") ?? "");
	if (!asked) return new Response("a range is asked for", { status: 400 });
	const start = Number(asked[1]);
	if (start >= served.byteLength) {
		return new Response(null, { status: 416, headers: { "content-range": `bytes */${served.byteLength}` } });
	}
	const end = Math.min(Number(asked[2]), served.byteLength - 1);
	return new Response(served.slice(start, end + 1), {
		status: 206,
		headers: { "content-range": `bytes ${start}-${end}/${served.byteLength}` },
	});
}

beforeAll(() => {
	server = Bun.serve({
		port: 0,
		hostname: "127.0.0.1",
		fetch(request) {
			const path = new URL(request.url).pathname;
			if (path === "/upload" && request.method === "PUT") return receivedUpload(request);
			if (path === "/source") return servedRange(request);
			return new Response("not here", { status: 404 });
		},
	});
});

afterAll(() => {
	server.stop(true);
});

afterEach(() => {
	globalThis.fetch = realFetch;
	received.length = 0;
	uploadBodies.length = 0;
});

function observeUploadBodies(): void {
	globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
		if (init?.method === "PUT") uploadBodies.push(init.body);
		return realFetch(input, init);
	}) as typeof fetch;
}

const sizes = [1, 1_465, 25_367, 32 * 1024 - 1, 32 * 1024, 300_000];

describe("a file chatd puts into the messenger's store", () => {
	for (const sizeBytes of sizes) {
		test(`of ${sizeBytes} bytes is streamed with its exact length, from a spooled copy`, async () => {
			served = patterned(sizeBytes, sizeBytes % 251);
			observeUploadBodies();
			const gateway = createBuzzPersonalGateway({} as never, { relayURL: `ws://127.0.0.1:${server.port}` });

			const kept = await gateway.uploadMedia(actor, { url: `http://127.0.0.1:${server.port}/source`, contentType: "application/pdf" });

			expect(uploadBodies).toHaveLength(1);
			expect(uploadBodies[0]).toBeInstanceOf(ReadableStream);
			expect(received).toEqual([
				{
					contentLength: String(sizeBytes),
					transferEncoding: null,
					declaredDigest: digestOf(served),
					digest: digestOf(served),
					sizeBytes,
				},
			]);
			expect(kept.digest).toBe(digestOf(served));
			expect(kept.sizeBytes).toBe(sizeBytes);
		});
	}

	for (const sizeBytes of [1_465, 300_000]) {
		test(`of ${sizeBytes} bytes held in memory is streamed with its exact length too`, async () => {
			const content = patterned(sizeBytes, 7);
			observeUploadBodies();

			const blob = await uploadBlob(`ws://127.0.0.1:${server.port}`, actor.secret, content, "application/pdf");

			expect(uploadBodies).toHaveLength(1);
			expect(uploadBodies[0]).toBeInstanceOf(ReadableStream);
			expect(received).toEqual([
				{
					contentLength: String(sizeBytes),
					transferEncoding: null,
					declaredDigest: digestOf(content),
					digest: digestOf(content),
					sizeBytes,
				},
			]);
			expect(blob.sha256).toBe(digestOf(content));
		});
	}
});
