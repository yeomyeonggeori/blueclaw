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

// A message spells the relay's host the way whoever uploaded the file reached
// it. An import run against localhost:3000 writes that, while the connector was
// told 127.0.0.1:3000, and the two are the same machine serving the same file.
// The question is whether the relay this session talks to is the one holding
// the file, not whether two strings match.
export function isServedByTheRelay(address: string, relayURL: string): boolean {
	const file = parsedURL(address);
	const relay = parsedURL(blossomBaseURL(relayURL));
	if (!file || !relay) return false;
	if (file.protocol !== relay.protocol || file.port !== relay.port) return false;
	if (file.hostname === relay.hostname) return true;
	return isLoopback(file.hostname) && isLoopback(relay.hostname);
}

function parsedURL(value: string): URL | null {
	try {
		return new URL(value);
	} catch {
		return null;
	}
}

function isLoopback(hostname: string): boolean {
	const name = hostname.replace(/^\[|\]$/g, "");
	return name === "localhost" || name === "::1" || /^127\./.test(name);
}

// The relay serves a blob only to a signed kind-24242 get event naming either
// the blob's hash or the serving host (BUD-11), from a key it knows as a
// member. The host is always named so one shape covers thumb variants too.
export function readAuthorizationHeader(userSecretHex: string, url: string): string {
	const nowSeconds = Math.floor(Date.now() / 1000);
	const tags = [
		["t", "get"],
		["expiration", String(nowSeconds + 300)],
		["server", new URL(url).origin],
	];
	const digestHex = blobDigestOf(url);
	if (digestHex !== "") tags.push(["x", digestHex]);
	const authEvent = finalizeEvent(
		{ kind: 24242, content: "get", created_at: nowSeconds, tags },
		hexToBytes(userSecretHex),
	);
	return "Nostr " + Buffer.from(JSON.stringify(authEvent)).toString("base64");
}

function blobDigestOf(url: string): string {
	const lastSegment = new URL(url).pathname.split("/").at(-1) ?? "";
	const beforeExtension = lastSegment.split(".")[0] ?? "";
	return /^[a-f0-9]{64}$/.test(beforeExtension) ? beforeExtension : "";
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
