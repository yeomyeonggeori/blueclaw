import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MediaNotConverted, withCanonicalMedia, type CanonicalMedia } from "../src/personal/media-canonical.ts";
import type { SpooledMedia } from "../src/personal/media-spool.ts";

let directory = "";

beforeAll(async () => {
	directory = await mkdtemp(join(tmpdir(), "media-canonical-test-"));
});

afterAll(async () => {
	await rm(directory, { recursive: true, force: true });
});

async function made(name: string, ffmpegArguments: string[]): Promise<SpooledMedia> {
	const path = join(directory, name);
	const run = Bun.spawnSync(["ffmpeg", "-y", "-loglevel", "error", ...ffmpegArguments, path]);
	if (run.exitCode !== 0) throw new Error(`could not make ${name}: ${run.stderr.toString()}`);
	const bytes = await readFile(path);
	return { path, digestHex: new Bun.CryptoHasher("sha256").update(bytes).digest("hex"), sizeBytes: (await stat(path)).size };
}

async function keptCopyOf(media: CanonicalMedia): Promise<{ media: CanonicalMedia; bytes: Buffer }> {
	return { media: { ...media }, bytes: await readFile(media.path) };
}

function topLevelBoxes(bytes: Buffer): string[] {
	const boxes: string[] = [];
	let offset = 0;
	while (offset + 8 <= bytes.length) {
		const size = bytes.readUInt32BE(offset);
		boxes.push(bytes.toString("latin1", offset + 4, offset + 8));
		if (size < 8) break;
		offset += size;
	}
	return boxes;
}

function videoCodecOf(path: string): string {
	const probe = Bun.spawnSync(["ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name", "-of", "csv=p=0", path]);
	return probe.stdout.toString().trim();
}

describe("a file the messenger keeps only in its canonical form", () => {
	test("a phone video with a location becomes a fast-start H.264 MP4 that names no place", async () => {
		const phoneVideo = await made("phone.mov", [
			"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=30",
			"-f", "lavfi", "-i", "sine=duration=1",
			"-c:v", "libx264", "-c:a", "aac", "-metadata", "location=+37.5665+126.9780/",
		]);

		const kept = await withCanonicalMedia(phoneVideo, "video/quicktime", keptCopyOf);

		expect(kept.media.contentType).toBe("video/mp4");
		expect(kept.media.sizeBytes).toBe(kept.bytes.length);
		expect(kept.media.digestHex).toBe(new Bun.CryptoHasher("sha256").update(kept.bytes).digest("hex"));
		const boxes = topLevelBoxes(kept.bytes);
		expect(boxes.indexOf("moov")).toBeLessThan(boxes.indexOf("mdat"));
		expect(kept.bytes.includes(Buffer.from("+37.5665"))).toBe(false);
		expect(kept.bytes.includes(Buffer.from("Lavf"))).toBe(false);
	});

	test("an HEVC video is re-encoded as H.264", async () => {
		const hevc = await made("hevc.mp4", ["-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=30", "-c:v", "libx265", "-tag:v", "hvc1"]);

		const keptPath = await withCanonicalMedia(hevc, "video/mp4", async (media) => {
			const copy = join(directory, "kept-from-hevc.mp4");
			await Bun.write(copy, Bun.file(media.path));
			return copy;
		});

		expect(videoCodecOf(keptPath)).toBe("h264");
	});

	test("a file that is not media is handed over as it was spooled", async () => {
		const document = await made("notes.png", ["-f", "lavfi", "-i", "testsrc=size=32x32", "-frames:v", "1"]);

		const kept = await withCanonicalMedia(document, "application/pdf", async (media) => media);

		expect(kept).toEqual({ ...document, contentType: "application/pdf" });
	});

	test("bytes ffmpeg cannot read fail with the reason, never as the original", async () => {
		const path = join(directory, "broken.mov");
		await Bun.write(path, "not a video");
		const broken = { path, digestHex: "0".repeat(64), sizeBytes: 11 };

		const failure = await withCanonicalMedia(broken, "video/quicktime", async () => "uploaded").catch((caught: unknown) => caught);

		expect(failure).toBeInstanceOf(MediaNotConverted);
	});
});
