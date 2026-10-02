import { readFileSync } from "node:fs";

export const relayCertificatePathVariable = "CHATD_BUZZ_RELAY_CA_PATH";
export const relayDialURLVariable = "CHATD_BUZZ_RELAY_DIAL_URL";
const relayURLVariable = "CHATD_BUZZ_RELAY_URL";

type Environment = Record<string, string | undefined>;
type RelayTLS = { ca: string };
type Dialled = { address: string; host?: string };

const certificates = new Map<string, string>();
const secureSchemes = new Set(["wss:", "https:"]);
const socketSchemes = new Set(["ws:", "wss:"]);

export function relayTLS(environment: Environment = Bun.env): RelayTLS | undefined {
	const path = environment[relayCertificatePathVariable]?.trim();
	if (!path) return undefined;
	const cached = certificates.get(path);
	if (cached !== undefined) return { ca: cached };
	const certificate = readFileSync(path, "utf8");
	certificates.set(path, certificate);
	return { ca: certificate };
}

export function dialledAddressOf(address: string, environment: Environment = Bun.env): Dialled {
	const dialURL = parsedURL(environment[relayDialURLVariable]);
	const publicURL = parsedURL(environment[relayURLVariable]);
	const target = parsedURL(address);
	if (!dialURL || !publicURL || !target || target.host !== publicURL.host) return { address };
	target.protocol = schemeFor(target.protocol, secureSchemes.has(dialURL.protocol));
	target.host = dialURL.host;
	return { address: target.toString(), host: publicURL.host };
}

function schemeFor(protocol: string, isSecure: boolean): string {
	if (socketSchemes.has(protocol)) return isSecure ? "wss:" : "ws:";
	return isSecure ? "https:" : "http:";
}

function parsedURL(value: string | undefined): URL | undefined {
	const trimmed = value?.trim();
	if (!trimmed || !URL.canParse(trimmed)) return undefined;
	return new URL(trimmed);
}

export function openRelaySocket(relayURL: string): WebSocket {
	const dialled = dialledAddressOf(relayURL);
	const tls = relayTLS();
	if (!tls && !dialled.host) return new WebSocket(dialled.address);
	const options = { ...(tls ? { tls } : {}), ...(dialled.host ? { headers: { Host: dialled.host } } : {}) };
	return Reflect.construct(WebSocket, [dialled.address, options]);
}

export function fetchFromRelay(address: string, init: RequestInit = {}): Promise<Response> {
	const dialled = dialledAddressOf(address);
	const tls = relayTLS();
	const asked = dialled.host ? { ...init, headers: withHost(init.headers, dialled.host) } : init;
	return tls ? fetch(dialled.address, { ...asked, tls }) : fetch(dialled.address, asked);
}

function withHost(given: HeadersInit | undefined, host: string): Headers {
	const headers = new Headers(given);
	headers.set("Host", host);
	return headers;
}
