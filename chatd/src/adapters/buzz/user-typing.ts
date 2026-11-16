import { withRelayAs } from "./relay-pool.ts";

export const TYPING_INDICATOR_KIND = 20002;

export async function announceTypingAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	threadRootID?: string;
	authTagJSON?: string;
}): Promise<void> {
	const tags = [["h", request.channelID]];
	if (request.threadRootID) tags.push(["e", request.threadRootID, "", "root"], ["e", request.threadRootID, "", "reply"]);
	await withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		await relay.publish(TYPING_INDICATOR_KIND, "", tags);
	});
}
