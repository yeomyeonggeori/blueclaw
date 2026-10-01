import { readFileSync } from "node:fs";

export const relayCertificatePathVariable = "CHATD_BUZZ_RELAY_CA_PATH";

type RelayTLS = { ca: string };

const certificates = new Map<string, string>();

export function relayTLS(environment: Record<string, string | undefined> = Bun.env): RelayTLS | undefined {
	const path = environment[relayCertificatePathVariable]?.trim();
	if (!path) return undefined;
	const cached = certificates.get(path);
	if (cached !== undefined) return { ca: cached };
	const certificate = readFileSync(path, "utf8");
	certificates.set(path, certificate);
	return { ca: certificate };
}

export function openRelaySocket(relayURL: string): WebSocket {
	const tls = relayTLS();
	if (!tls) return new WebSocket(relayURL);
	return Reflect.construct(WebSocket, [relayURL, { tls }]);
}

export function fetchFromRelay(address: string, init: RequestInit = {}): Promise<Response> {
	const tls = relayTLS();
	return tls ? fetch(address, { ...init, tls }) : fetch(address, init);
}
