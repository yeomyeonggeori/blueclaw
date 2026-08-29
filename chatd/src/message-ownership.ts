import type { BuzzAdapter } from "./adapters/buzz/adapter.ts";
import { firstTagValue } from "./adapters/buzz/types.ts";

export const messageChangeMatrix =
	"a message the assistant sent, your own, or anyone's if you hold the channel admin role";

export class MessageChangeRefused extends Error {
	constructor(message: string) {
		super(message);
		this.name = "MessageChangeRefused";
	}
}

export function mayChangeMessage(
	targetAuthorPubkeyHex: string,
	actorPubkeyHex: string,
	actorIsElevated: boolean,
	botPubkeyHex: string,
): boolean {
	if (targetAuthorPubkeyHex === botPubkeyHex) return true;
	if (actorPubkeyHex !== "" && targetAuthorPubkeyHex === actorPubkeyHex) return true;
	return actorIsElevated;
}

export async function requireMessageChangeAllowed(
	adapter: BuzzAdapter,
	messageID: string,
	requesterPubkeyHex: string | undefined,
): Promise<void> {
	const target = await adapter.readMessageEvent(messageID);
	if (!target) {
		throw new MessageChangeRefused(
			`message ${messageID} cannot be read from this relay, so who may change it cannot be decided`,
		);
	}
	const actorPubkeyHex = (requesterPubkeyHex ?? "").trim();
	const channelID = firstTagValue(target, "h") ?? "";
	const actorIsElevated = await isElevatedIn(adapter, channelID, actorPubkeyHex);
	if (mayChangeMessage(target.pubkey, actorPubkeyHex, actorIsElevated, adapter.botPubkey)) return;
	throw new MessageChangeRefused(`you may change ${messageChangeMatrix}, and message ${messageID} is none of those`);
}

export async function isElevatedIn(
	adapter: BuzzAdapter,
	channelID: string,
	actorPubkeyHex: string,
): Promise<boolean> {
	if (actorPubkeyHex === "" || channelID === "") return false;
	return (await adapter.channelElevatedPubkeys(channelID)).has(actorPubkeyHex);
}
