import { asAHostService } from "./service-credential.ts";
import type { BuzzAdapter } from "./adapters/buzz/adapter.ts";
import type { BuzzEvent } from "./adapters/buzz/types.ts";
import { pubkeyFromSecret } from "./adapters/buzz/user-session.ts";
import { MalformedRequest } from "./personal/parse.ts";

export class MessageChangeRefused extends Error {
	constructor(message: string) {
		super(message);
		this.name = "MessageChangeRefused";
	}
}

export class MessageReadRefused extends Error {
	constructor(message: string) {
		super(message);
		this.name = "MessageReadRefused";
	}
}

const signingSecretPath = "/admin/api/buzz/signing-secret";
export const secretHexPattern = /^[0-9a-f]{64}$/;

export async function signerForMessageChange(
	adapter: BuzzAdapter,
	target: BuzzEvent,
	requesterPubkeyHex: string | undefined,
	admindBaseURL: string | undefined,
): Promise<string | undefined> {
	const messageID = target.id;
	if (target.pubkey === adapter.botPubkey) return undefined;
	const pubkeyHex = (requesterPubkeyHex ?? "").trim().toLowerCase();
	if (!admindBaseURL || !secretHexPattern.test(pubkeyHex)) {
		throw new MessageChangeRefused(
			`message ${messageID} was written by somebody else and this device cannot sign as the person who asked`,
		);
	}
	return signingSecretFor(pubkeyHex, admindBaseURL, messageID);
}

export function signingKeyring(admindBaseURL: string | undefined): (author: string, id: string) => Promise<string> {
	const held = new Map<string, Promise<string>>();
	return (authorPubkeyHex, messageID) => {
		const known = held.get(authorPubkeyHex);
		if (known) return known;
		const asked = signingSecretFor(authorPubkeyHex, admindBaseURL, messageID);
		held.set(authorPubkeyHex, asked);
		return asked;
	};
}

export async function signingSecretFor(
	authorPubkeyHex: string,
	admindBaseURL: string | undefined,
	messageID: string,
): Promise<string> {
	const pubkeyHex = authorPubkeyHex.trim().toLowerCase();
	if (!admindBaseURL || !secretHexPattern.test(pubkeyHex)) {
		throw new MessageChangeRefused(
			`message ${messageID} was written by somebody else and this device cannot sign as the person who wrote it`,
		);
	}
	return await heldSigningSecret(
		pubkeyHex,
		admindBaseURL,
		(reason) => new MessageChangeRefused(`message ${messageID} cannot be changed: ${reason}`),
	);
}

export async function requesterSigningSecret(
	requesterPubkeyHex: unknown,
	admindBaseURL: string | undefined,
	refused: (reason: string) => Error,
): Promise<string> {
	const pubkeyHex = typeof requesterPubkeyHex === "string" ? requesterPubkeyHex.trim().toLowerCase() : "";
	if (!secretHexPattern.test(pubkeyHex)) {
		throw new MalformedRequest("requesterPubkeyHex must name the person who asked, as 64 hexadecimal characters");
	}
	const refusedAs = (reason: string) => refused(`this device cannot act as ${pubkeyHex}: ${reason}`);
	if (!admindBaseURL) throw refusedAs("no service holding signing keys is configured");
	return heldSigningSecret(pubkeyHex, admindBaseURL, refusedAs);
}

export async function heldSigningSecret(
	pubkeyHex: string,
	admindBaseURL: string,
	refused: (reason: string) => Error,
): Promise<string> {
	const secretHex = await askAdmindForSigningSecret(admindBaseURL, pubkeyHex, refused);
	if (pubkeyFromSecret(secretHex) !== pubkeyHex) {
		throw refused(`the signing key this device was handed is not the key ${pubkeyHex} signs with`);
	}
	return secretHex;
}

async function askAdmindForSigningSecret(
	admindBaseURL: string,
	pubkeyHex: string,
	refused: (reason: string) => Error,
): Promise<string> {
	let response: Response;
	try {
		response = await fetch(`${admindBaseURL}${signingSecretPath}`, asAHostService({
			method: "POST",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({ pubkeyHex }),
		}));
	} catch (error) {
		throw refused(`this device cannot reach the service that holds ${pubkeyHex}'s signing key (${String(error)})`);
	}
	if (!response.ok) {
		throw refused(`this device holds no signing key for ${pubkeyHex} (answered ${response.status})`);
	}
	const document = (await response.json()) as { secretHex?: unknown };
	const secretHex = typeof document.secretHex === "string" ? document.secretHex.trim().toLowerCase() : "";
	if (!secretHexPattern.test(secretHex)) {
		throw refused(`the answer for ${pubkeyHex} carried no usable signing key`);
	}
	return secretHex;
}

export async function isElevatedIn(
	adapter: BuzzAdapter,
	channelID: string,
	actorPubkeyHex: string,
	freshly = false,
): Promise<boolean> {
	if (actorPubkeyHex === "" || channelID === "") return false;
	return (await adapter.channelElevatedPubkeys(channelID, freshly)).has(actorPubkeyHex);
}
