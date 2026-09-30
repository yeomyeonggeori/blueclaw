import { withRelayAs } from "./relay-pool.ts";

export const TYPING_INDICATOR_KIND = 20002;

export async function announceTypingAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	authTagJSON?: string;
}): Promise<void> {
	await withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		await relay.publish(TYPING_INDICATOR_KIND, "", [["h", request.channelID]]);
	});
}
