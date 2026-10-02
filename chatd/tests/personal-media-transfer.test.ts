import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtemp, open, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { verifyEvent } from "nostr-tools/pure";

const mebibyte = 1024 * 1024;
const fileBytes = 320 * mebibyte;
const boundedGrowthBytes = fileBytes * 0.75;

function chunkAt(index: number): Uint8Array {
	const chunk = new Uint8Array(mebibyte);
	for (let offset = 0; offset < chunk.length; offset += 1) chunk[offset] = (offset * 31 + index * 7) & 0xff;
	return chunk;
}

async function writeGeneratedFile(path: string): Promise<string> {
	const hasher = new Bun.CryptoHasher("sha256");
	const file = await open(path, "w");
	for (let index = 0; index * mebibyte < fileBytes; index += 1) {
		const chunk = chunkAt(index);
		hasher.update(chunk);
		await file.write(chunk);
	}
	await file.close();
	return hasher.digest("hex");
}

type Received = { declaredDigest: string; digest: string; sizeBytes: number; authorization: string };

const received: Received[] = [];
const rangesAsked: string[] = [];

function servedRange(request: Request, servedBytes: number): Response {
	const asked = /^bytes=(\d+)-(\d+)$/.exec(request.headers.get("range") ?? "");
	if (!asked) return new Response("a range is asked for, always", { status: 400 });
	rangesAsked.push(asked[0]);
	const start = Number(asked[1]);
	const end = Math.min(Number(asked[2]), servedBytes - 1);
	if (start >= servedBytes) return new Response(null, { status: 416, headers: { "content-range": `bytes */${servedBytes}` } });
	return new Response(Bun.file(sourcePath).slice(start, end + 1), {
		status: 206,
		headers: { "content-type": "video/mp4", "content-range": `bytes ${start}-${end}/${servedBytes}` },
	});
}
let server: ReturnType<typeof Bun.serve>;
let directory = "";
let sourcePath = "";
let expectedDigest = "";

beforeAll(async () => {
	directory = await mkdtemp(join(tmpdir(), "chatd-media-transfer-"));
	sourcePath = join(directory, "source.mp4");
	expectedDigest = await writeGeneratedFile(sourcePath);
	server = Bun.serve({
		port: 0,
		maxRequestBodySize: 2 * fileBytes,
		hostname: "127.0.0.1",
		async fetch(request) {
			const path = new URL(request.url).pathname;
			if (path === "/source" || path === `/${expectedDigest}.mp4`) return servedRange(request, fileBytes);
			if (path === "/warm") return servedRange(request, mebibyte);
			if (path === "/upload" && request.method === "PUT" && request.body) {
				const hasher = new Bun.CryptoHasher("sha256");
				let sizeBytes = 0;
				for await (const chunk of request.body) {
					hasher.update(chunk);
					sizeBytes += chunk.byteLength;
				}
				const digest = hasher.digest("hex");
				received.push({
					declaredDigest: request.headers.get("x-sha-256") ?? "",
					digest,
					sizeBytes,
					authorization: request.headers.get("authorization") ?? "",
				});
				return Response.json({ url: `http://127.0.0.1:${server.port}/${digest}.mp4`, sha256: digest, size: sizeBytes, type: "video/mp4" });
			}
			return new Response("not here", { status: 404 });
		},
	});
});

afterAll(async () => {
	server.stop(true);
	await rm(directory, { recursive: true, force: true });
});

describe("a file larger than any frame or bucket default moves through chatd", () => {
	test("both ways, unchanged, without chatd holding it", async () => {
		const origin = `http://127.0.0.1:${server.port}`;
		const probe = Bun.spawn(
			[process.execPath, "run", `${import.meta.dir}/fixtures/media-transfer-probe.ts`, `ws://127.0.0.1:${server.port}`, `${origin}/source`, `${origin}/${expectedDigest}.mp4`, `${origin}/warm`],
			{ stdout: "pipe", stderr: "inherit" },
		);
		const reported = JSON.parse((await new Response(probe.stdout).text()).trim()) as {
			uploadStatus: number;
			upload: { media: { address: string; digest: string; sizeBytes: number } };
			readStatus: number;
			readRange: string | null;
			read: { digest: string; sizeBytes: number };
			baselineResidentBytes: number;
			peakResidentBytes: number;
		};
		expect(await probe.exited).toBe(0);

		expect(reported.uploadStatus).toBe(200);
		const large = received.find((one) => one.sizeBytes === fileBytes);
		expect(large?.digest).toBe(expectedDigest);
		expect(large?.declaredDigest).toBe(expectedDigest);
		const authEvent = JSON.parse(Buffer.from((large?.authorization ?? "").slice("Nostr ".length), "base64").toString());
		expect(verifyEvent(authEvent)).toBe(true);
		expect(authEvent.tags).toContainEqual(["x", expectedDigest]);
		expect(reported.upload.media as unknown).toEqual({
			address: `${origin}/${expectedDigest}.mp4`,
			digest: expectedDigest,
			sizeBytes: fileBytes,
			contentType: "video/mp4",
		});

		expect(reported.readStatus).toBe(206);
		expect(reported.readRange).toBe(`bytes 0-1023/${fileBytes}`);
		expect(rangesAsked.every((range) => /^bytes=\d+-\d+$/.test(range))).toBe(true);
		expect(reported.read).toEqual({ digest: expectedDigest, sizeBytes: fileBytes });

		const growthBytes = reported.peakResidentBytes - reported.baselineResidentBytes;
		console.log(`chatd moved ${fileBytes} bytes each way; resident memory grew by ${(growthBytes / mebibyte).toFixed(1)} MiB at its peak`);
		expect(growthBytes).toBeLessThan(boundedGrowthBytes);
	}, 120_000);
});
