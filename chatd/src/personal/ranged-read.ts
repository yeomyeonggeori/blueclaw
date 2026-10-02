export const rangeBytes = 6 * 1024 * 1024;

const contentRangePattern = /^bytes (?:(\d+)-(\d+)|\*)\/(\d+)$/;

export type FetchRange = (rangeHeader: string) => Promise<Response>;

export class RangeRefused extends Error {
	constructor(readonly status: number, detail: string) {
		super(detail);
		this.name = "RangeRefused";
	}
}

export async function* rangesOf(fetchRange: FetchRange): AsyncGenerator<Uint8Array> {
	let start = 0;
	for (;;) {
		const response = await fetchRange(`bytes=${start}-${start + rangeBytes - 1}`);
		if (response.status === 416) return;
		if (response.status === 200) {
			yield await wholeBodyWithinOneRange(response);
			return;
		}
		if (response.status !== 206) throw new RangeRefused(response.status, `the source answered ${response.status} for bytes from ${start}`);
		const totalBytes = totalBytesOf(response);
		const bytes = new Uint8Array(await response.arrayBuffer());
		yield bytes;
		start += bytes.byteLength;
		if (bytes.byteLength === 0 || start >= totalBytes) return;
	}
}

async function wholeBodyWithinOneRange(response: Response): Promise<Uint8Array> {
	const declared = Number(response.headers.get("content-length") ?? "0");
	if (declared > rangeBytes) {
		throw new RangeRefused(response.status, `the source ignored the range and offered ${declared} bytes at once`);
	}
	return new Uint8Array(await response.arrayBuffer());
}

function totalBytesOf(response: Response): number {
	const matched = contentRangePattern.exec(response.headers.get("content-range") ?? "");
	if (!matched) throw new RangeRefused(response.status, "the source answered a range without saying the whole size");
	return Number(matched[3]);
}
