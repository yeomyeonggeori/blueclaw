import type { BuzzAdapter } from "./adapters/buzz/adapter.ts";
import { deriveBuzzSecret } from "./adapters/buzz/identity.ts";

export class MessageChangeRefused extends Error {
	constructor(message: string) {
		super(message);
		this.name = "MessageChangeRefused";
	}
}

// The relay accepts an edit or a deletion only from the key that authored the
// event, which makes it the single judge of who may change what. chatd decides
// nothing here: it picks the key to sign with. The assistant's own messages are
// signed by the assistant; everything else is signed as the person who asked,
// whose key this device derives from the same seed admind uses.
export async function signerForMessageChange(
	adapter: BuzzAdapter,
	messageID: string,
	requesterEmail: string | undefined,
	keySeed: string | undefined,
): Promise<string | undefined> {
	const target = await adapter.readMessageEvent(messageID);
	if (!target) {
		throw new MessageChangeRefused(`message ${messageID} cannot be read from this relay, so it cannot be changed`);
	}
	if (target.pubkey === adapter.botPubkey) return undefined;
	const email = (requesterEmail ?? "").trim();
	if (!keySeed || email === "") {
		throw new MessageChangeRefused(
			`message ${messageID} was written by somebody else and this device cannot sign as the person who asked`,
		);
	}
	return deriveBuzzSecret(keySeed, email);
}

export async function isElevatedIn(
	adapter: BuzzAdapter,
	channelID: string,
	actorPubkeyHex: string,
): Promise<boolean> {
	if (actorPubkeyHex === "" || channelID === "") return false;
	return (await adapter.channelElevatedPubkeys(channelID)).has(actorPubkeyHex);
}
