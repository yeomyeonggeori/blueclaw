import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { SpooledMedia } from "./media-spool.ts";

export type CanonicalMedia = SpooledMedia & { contentType: string };

export class MediaNotConverted extends Error {
	constructor(contentType: string, reason: string) {
		super(`the company host could not convert this ${contentType} file into one the messenger keeps: ${reason}`);
		this.name = "MediaNotConverted";
	}
}

const conversionTimeoutMilliseconds = 10 * 60 * 1000;

const canonicalVideoType = "video/mp4";
const canonicalVideoArguments = [
	"-map", "0:v:0", "-map", "0:a:0?",
	"-map_metadata", "-1", "-map_chapters", "-1", "-sn", "-dn",
	"-fflags", "+bitexact", "-flags:v", "+bitexact", "-flags:a", "+bitexact",
	"-c:v", "libx264", "-preset", "fast", "-crf", "23", "-pix_fmt", "yuv420p",
	"-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2",
	"-c:a", "aac", "-b:a", "128k",
	"-movflags", "+faststart", "-metadata", "encoder=",
];

export async function withCanonicalMedia<Result>(
	spooled: SpooledMedia,
	contentType: string,
	use: (media: CanonicalMedia) => Promise<Result>,
): Promise<Result> {
	if (!contentType.startsWith("video/")) return use({ ...spooled, contentType });
	const directory = await mkdtemp(join(tmpdir(), "chatd-canonical-"));
	try {
		const output = join(directory, "media.mp4");
		await convert(spooled.path, output, contentType);
		return await use({ ...(await described(output)), contentType: canonicalVideoType });
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}

async function convert(source: string, output: string, contentType: string): Promise<void> {
	const ffmpeg = startFfmpeg(["-i", source, ...canonicalVideoArguments, output], contentType);
	const [exitCode, errorOutput] = await Promise.all([ffmpeg.exited, new Response(ffmpeg.stderr).text()]);
	if (exitCode === 0) return;
	const lastLine = errorOutput.trim().split("\n").at(-1) ?? "";
	throw new MediaNotConverted(contentType, lastLine || `ffmpeg exited with ${exitCode}`);
}

function startFfmpeg(conversionArguments: string[], contentType: string) {
	try {
		return Bun.spawn(["ffmpeg", "-y", "-nostdin", "-loglevel", "error", "-protocol_whitelist", "file,pipe", ...conversionArguments], {
			stdin: "ignore",
			stdout: "ignore",
			stderr: "pipe",
			timeout: conversionTimeoutMilliseconds,
		});
	} catch (error) {
		throw new MediaNotConverted(contentType, `ffmpeg could not be started (${String(error)}); install ffmpeg on the company host`);
	}
}

async function described(path: string): Promise<SpooledMedia> {
	const hasher = new Bun.CryptoHasher("sha256");
	for await (const chunk of Bun.file(path).stream()) hasher.update(chunk);
	return { path, digestHex: hasher.digest("hex"), sizeBytes: (await stat(path)).size };
}
