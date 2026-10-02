import { mkdtemp, open, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

export type SpooledMedia = {
	path: string;
	digestHex: string;
	sizeBytes: number;
};

export async function withSpooledMedia<Result>(
	source: AsyncIterable<Uint8Array>,
	use: (spooled: SpooledMedia) => Promise<Result>,
): Promise<Result> {
	const directory = await mkdtemp(join(tmpdir(), "chatd-media-"));
	try {
		return await use(await spool(source, join(directory, "media")));
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}

async function spool(source: AsyncIterable<Uint8Array>, path: string): Promise<SpooledMedia> {
	const file = await open(path, "w", 0o600);
	const hasher = new Bun.CryptoHasher("sha256");
	let sizeBytes = 0;
	try {
		for await (const chunk of source) {
			hasher.update(chunk);
			await file.write(chunk);
			sizeBytes += chunk.byteLength;
		}
	} finally {
		await file.close();
	}
	return { path, digestHex: hasher.digest("hex"), sizeBytes };
}
