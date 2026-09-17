import { finalizeEvent, getPublicKey } from "nostr-tools/pure";
import { createRelayConnection, type RelayClientTiming, type RelayConnection } from "./relay-connection.ts";
import type { BuzzEvent } from "./types.ts";

export type BuzzRelayClient = RelayConnection;
export type { RelayClientTiming } from "./relay-connection.ts";

export function createBuzzRelayClient(
	relayURL: string,
	privateKeyHex: string,
	authTagJSON?: string,
	timing?: RelayClientTiming,
): BuzzRelayClient {
	const secretKey = hexToBytes(privateKeyHex);
	const pubkeyHex = getPublicKey(secretKey);
	const signEvent = (kind: number, content: string, tags: string[][]): BuzzEvent =>
		finalizeEvent({ kind, content, tags, created_at: Math.floor(Date.now() / 1000) }, secretKey) as BuzzEvent;
	return createRelayConnection(relayURL, { pubkeyHex, signEvent }, authTagJSON, timing);
}

function hexToBytes(hex: string): Uint8Array {
	const bytes = new Uint8Array(hex.length / 2);
	for (let index = 0; index < bytes.length; index++) {
		bytes[index] = Number.parseInt(hex.slice(index * 2, index * 2 + 2), 16);
	}
	return bytes;
}
