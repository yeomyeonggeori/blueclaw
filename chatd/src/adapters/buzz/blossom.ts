import { finalizeEvent } from "nostr-tools/pure";

export type BlossomBlob = {
	url: string;
	sha256: string;
	size: number;
	mimeType: string;
};

export class BlobRefused extends Error {
	constructor(
		readonly status: number,
		readonly reason: string,
	) {
		super(`blossom upload returned ${status}: ${reason}`);
		this.name = "BlobRefused";
	}

	get willRefuseAgain(): boolean {
		return this.status < 500 && this.status !== 429;
	}
}

export function blossomBaseURL(relayURL: string): string {
	if (relayURL.startsWith("wss://")) return "https://" + relayURL.slice("wss://".length);
	if (relayURL.startsWith("ws://")) return "http://" + relayURL.slice("ws://".length);
	return relayURL;
}

export function imetaTag(blob: BlossomBlob, filename?: string): string[] {
	const tag = [
		"imeta",
		"url " + blob.url,
		"m " + blob.mimeType,
		"x " + blob.sha256,
		"size " + String(blob.size),
	];
	const named = (filename ?? "").trim();
	if (named !== "") tag.push("filename " + named);
	return tag;
}

export async function uploadBlob(
	relayURL: string,
	userSecretHex: string,
	content: Uint8Array,
	mimeType: string,
): Promise<BlossomBlob> {
	const digestHex = new Bun.CryptoHasher("sha256").update(content).digest("hex");
	const nowSeconds = Math.floor(Date.now() / 1000);
	const authEvent = finalizeEvent(
		{
			kind: 24242,
			content: "upload",
			created_at: nowSeconds,
			tags: [
				["t", "upload"],
				["x", digestHex],
				["expiration", String(nowSeconds + 3600)],
			],
		},
		hexToBytes(userSecretHex),
	);
	const authorization = "Nostr " + Buffer.from(JSON.stringify(authEvent)).toString("base64");
	const body = new ArrayBuffer(content.byteLength);
	new Uint8Array(body).set(content);
	const response = await fetch(blossomBaseURL(relayURL) + "/upload", {
		method: "PUT",
		headers: {
			Authorization: authorization,
			"Content-Type": mimeType,
			"X-SHA-256": digestHex,
		},
		body,
	});
	if (!response.ok) {
		throw new BlobRefused(response.status, (await response.text()).trim());
	}
	const blob = parseBlobResponse(await response.json());
	return {
		url: blob.url,
		sha256: blob.sha256 || digestHex,
		size: blob.size || content.byteLength,
		mimeType: blob.mimeType || mimeType,
	};
}

function parseBlobResponse(value: unknown): BlossomBlob {
	if (typeof value !== "object" || value === null) {
		throw new Error("blossom upload returned a non-object response");
	}
	const record = value as Record<string, unknown>;
	return {
		url: typeof record.url === "string" ? record.url : "",
		sha256: typeof record.sha256 === "string" ? record.sha256 : "",
		size: typeof record.size === "number" ? record.size : 0,
		mimeType: typeof record.type === "string" ? record.type : "",
	};
}

function hexToBytes(hex: string): Uint8Array {
	const bytes = new Uint8Array(hex.length / 2);
	for (let index = 0; index < bytes.length; index++) {
		bytes[index] = Number.parseInt(hex.slice(index * 2, index * 2 + 2), 16);
	}
	return bytes;
}
