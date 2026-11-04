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

const canonicalPictureType = "image/jpeg";
const heifPictureTypes = new Set(["image/heic", "image/heif"]);
const canonicalPictureArguments = [
	"-map_metadata", "-1", "-vf", "sidedata=mode=delete:type=ICC_PROFILE",
	"-fflags", "+bitexact", "-flags:v", "+bitexact", "-q:v", "2",
];

type Conversion = {
	contentType: string;
	convert: (source: string, directory: string, contentType: string) => Promise<string>;
};

export async function withCanonicalMedia<Result>(
	spooled: SpooledMedia,
	contentType: string,
	use: (media: CanonicalMedia) => Promise<Result>,
): Promise<Result> {
	const conversion = conversionOf(contentType);
	if (!conversion) return use({ ...spooled, contentType });
	const directory = await mkdtemp(join(tmpdir(), "chatd-canonical-"));
	try {
		const output = await conversion.convert(spooled.path, directory, contentType);
		return await use({ ...(await described(output)), contentType: conversion.contentType });
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}

function conversionOf(contentType: string): Conversion | undefined {
	if (contentType.startsWith("video/")) return { contentType: canonicalVideoType, convert: convertVideo };
	if (heifPictureTypes.has(contentType)) return { contentType: canonicalPictureType, convert: convertPicture };
	return undefined;
}

async function convertVideo(source: string, directory: string, contentType: string): Promise<string> {
	const output = join(directory, "media.mp4");
	await runFfmpeg(["-i", source, ...canonicalVideoArguments, output], contentType);
	return output;
}

async function convertPicture(source: string, directory: string, contentType: string): Promise<string> {
	const decoded = join(directory, "decoded.png");
	const output = join(directory, "media.jpg");
	await run(["heif-convert", source, decoded], contentType);
	await runFfmpeg(["-i", decoded, ...canonicalPictureArguments, output], contentType);
	return output;
}

function runFfmpeg(conversionArguments: string[], contentType: string): Promise<void> {
	return run(["ffmpeg", "-y", "-nostdin", "-loglevel", "error", "-protocol_whitelist", "file,pipe", ...conversionArguments], contentType);
}

async function run(command: string[], contentType: string): Promise<void> {
	const child = start(command, contentType);
	const [exitCode, errorOutput] = await Promise.all([child.exited, new Response(child.stderr).text()]);
	if (exitCode === 0) return;
	const lastLine = errorOutput.trim().split("\n").at(-1) ?? "";
	throw new MediaNotConverted(contentType, lastLine || `${command[0]} exited with ${exitCode}`);
}

function start(command: string[], contentType: string) {
	try {
		return Bun.spawn(command, {
			stdin: "ignore",
			stdout: "ignore",
			stderr: "pipe",
			timeout: conversionTimeoutMilliseconds,
		});
	} catch (error) {
		throw new MediaNotConverted(contentType, `${command[0]} could not be started (${String(error)}); install ${command[0]} on the company host`);
	}
}

async function described(path: string): Promise<SpooledMedia> {
	const hasher = new Bun.CryptoHasher("sha256");
	for await (const chunk of Bun.file(path).stream()) hasher.update(chunk);
	return { path, digestHex: hasher.digest("hex"), sizeBytes: (await stat(path)).size };
}
