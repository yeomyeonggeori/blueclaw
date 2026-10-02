import { createOutboundHandler } from "../../src/outbound.ts";
import { createBuzzPersonalGateway } from "../../src/personal/buzz.ts";
import { rangesOf } from "../../src/personal/ranged-read.ts";

const [relayURL = "", sourceURL = "", mediaURL = "", warmURL = ""] = process.argv.slice(2);
const actor = { kind: "buzz-secret", secret: "1".repeat(64) };
const handler = createOutboundHandler({ buzz: {} } as never, {} as never, {
	buzz: createBuzzPersonalGateway({} as never, { relayURL }),
});

let peakResidentBytes = 0;
const sampler = setInterval(() => {
	peakResidentBytes = Math.max(peakResidentBytes, process.memoryUsage().rss);
}, 10);

function ask(capability: string, body: unknown): Promise<Response> {
	return handler(
		new Request(`http://127.0.0.1/v1/platform/buzz/${capability}`, { method: "POST", body: JSON.stringify(body) }),
	);
}

async function digestOfRanges(chunks: AsyncIterable<Uint8Array>): Promise<{ digest: string; sizeBytes: number }> {
	const hasher = new Bun.CryptoHasher("sha256");
	let sizeBytes = 0;
	for await (const chunk of chunks) {
		hasher.update(chunk);
		sizeBytes += chunk.byteLength;
	}
	return { digest: hasher.digest("hex"), sizeBytes };
}

await (await ask("person.media.upload", { actor, sourceURL: warmURL, contentType: "video/mp4" })).arrayBuffer();
Bun.gc(true);
const baselineResidentBytes = process.memoryUsage().rss;
peakResidentBytes = baselineResidentBytes;

const uploaded = await ask("person.media.upload", { actor, sourceURL, contentType: "video/mp4" });
const uploadAnswer = await uploaded.json();

const firstRange = await ask("person.media.read", { actor, mediaURL, range: "bytes=0-1023" });
const readAnswer = await digestOfRanges(rangesOf((range) => ask("person.media.read", { actor, mediaURL, range })));

clearInterval(sampler);
console.log(
	JSON.stringify({
		uploadStatus: uploaded.status,
		upload: uploadAnswer,
		readStatus: firstRange.status,
		readRange: firstRange.headers.get("content-range"),
		read: readAnswer,
		baselineResidentBytes,
		peakResidentBytes,
	}),
);
