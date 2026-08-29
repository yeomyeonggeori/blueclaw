import type { BuzzAdapter } from "./adapters/buzz/adapter.ts";
import { pubkeyFromSecret } from "./adapters/buzz/user-session.ts";

export class MessageChangeRefused extends Error {
	constructor(message: string) {
		super(message);
		this.name = "MessageChangeRefused";
	}
}

const signingSecretPath = "/admin/api/buzz/signing-secret";
const secretHexPattern = /^[0-9a-f]{64}$/;

export async function signerForMessageChange(
	adapter: BuzzAdapter,
	messageID: string,
	requesterPubkeyHex: string | undefined,
	admindBaseURL: string | undefined,
): Promise<string | undefined> {
	const target = await adapter.readMessageEvent(messageID);
	if (!target) {
		throw new MessageChangeRefused(`message ${messageID} cannot be read from this relay, so it cannot be changed`);
	}
	if (target.pubkey === adapter.botPubkey) return undefined;
	const pubkeyHex = (requesterPubkeyHex ?? "").trim().toLowerCase();
	if (!admindBaseURL || !secretHexPattern.test(pubkeyHex)) {
		throw new MessageChangeRefused(
			`message ${messageID} was written by somebody else and this device cannot sign as the person who asked`,
		);
	}
	const secretHex = await askAdmindForSigningSecret(admindBaseURL, pubkeyHex, messageID);
	if (pubkeyFromSecret(secretHex) !== pubkeyHex) {
		throw new MessageChangeRefused(
			`message ${messageID} cannot be changed: the signing key this device was handed is not the key ${pubkeyHex} signs with`,
		);
	}
	return secretHex;
}

async function askAdmindForSigningSecret(
	admindBaseURL: string,
	pubkeyHex: string,
	messageID: string,
): Promise<string> {
	let response: Response;
	try {
		response = await fetch(`${admindBaseURL}${signingSecretPath}`, {
			method: "POST",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({ pubkeyHex }),
		});
	} catch (error) {
		throw new MessageChangeRefused(
			`message ${messageID} cannot be changed: this device cannot reach the service that holds ${pubkeyHex}'s signing key (${String(error)})`,
		);
	}
	if (!response.ok) {
		throw new MessageChangeRefused(
			`message ${messageID} cannot be changed: this device holds no signing key for ${pubkeyHex} (answered ${response.status})`,
		);
	}
	const document = (await response.json()) as { secretHex?: unknown };
	const secretHex = typeof document.secretHex === "string" ? document.secretHex.trim().toLowerCase() : "";
	if (!secretHexPattern.test(secretHex)) {
		throw new MessageChangeRefused(
			`message ${messageID} cannot be changed: the answer for ${pubkeyHex} carried no usable signing key`,
		);
	}
	return secretHex;
}

export async function isElevatedIn(
	adapter: BuzzAdapter,
	channelID: string,
	actorPubkeyHex: string,
): Promise<boolean> {
	if (actorPubkeyHex === "" || channelID === "") return false;
	return (await adapter.channelElevatedPubkeys(channelID)).has(actorPubkeyHex);
}
