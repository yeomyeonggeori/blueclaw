import { afterAll, mock } from "bun:test";
import * as relayClient from "../src/adapters/buzz/relay-client.ts";

const relayClientPath = "../src/adapters/buzz/relay-client.ts";
const realRelayClient = { ...relayClient };

type RelayClientFactory = (relayURL: string, privateKeyHex: string, authTagJSON?: string) => unknown;

// Bun keeps a mock.module replacement for every test file after it in the same process and has no unmock (oven-sh/bun#7823).
export function replaceRelayClientInThisFile(createBuzzRelayClient: RelayClientFactory): void {
	mock.module(relayClientPath, () => ({ ...realRelayClient, createBuzzRelayClient }));
	afterAll(() => {
		mock.module(relayClientPath, () => realRelayClient);
	});
}
