import { readFileSync } from "node:fs";

export const relayCertificatePathVariable = "CHATD_BUZZ_RELAY_CA_PATH";
export const relayAddressVariable = "CHATD_BUZZ_RELAY_ADDRESS";
export const relayURLVariable = "CHATD_BUZZ_RELAY_URL";
export const messengerURLVariable = "RELAY_URL";

type RelayTLS = { ca: string };

type Environment = Record<string, string | undefined>;

const certificates = new Map<string, string>();

export function relayTLS(environment: Environment = Bun.env): RelayTLS | undefined {
	const path = environment[relayCertificatePathVariable]?.trim();
	if (!path) return undefined;
	const cached = certificates.get(path);
	if (cached !== undefined) return { ca: cached };
	const certificate = readFileSync(path, "utf8");
	certificates.set(path, certificate);
	return { ca: certificate };
}

export function relayURLOf(environment: Environment): string | undefined {
	return environment[relayURLVariable]?.trim() || environment[messengerURLVariable]?.trim() || undefined;
}

export type RelayDial = { target: string; host: string | undefined };

export function relayDialOf(address: string, environment: Environment = Bun.env): RelayDial {
	const through = environment[relayAddressVariable]?.trim();
	const named = relayURLOf(environment);
	if (!through || !named) return { target: address, host: undefined };
	const asked = new URL(address);
	if (asked.host !== new URL(named).host) return { target: address, host: undefined };
	const reached = new URL(through);
	const scheme = schemeReaching(asked.protocol, reached.protocol);
	return { target: `${scheme}//${reached.host}${asked.pathname}${asked.search}`, host: asked.host };
}

function schemeReaching(asked: string, reached: string): string {
	if (asked === "ws:" || asked === "wss:") return reached;
	return reached === "wss:" || reached === "https:" ? "https:" : "http:";
}

export function openRelaySocket(relayURL: string): WebSocket {
	const tls = relayTLS();
	const dial = relayDialOf(relayURL);
	const options = { ...(tls ? { tls } : {}), ...(dial.host ? { headers: { Host: dial.host } } : {}) };
	if (Object.keys(options).length === 0) return new WebSocket(dial.target);
	return Reflect.construct(WebSocket, [dial.target, options]);
}

export function fetchFromRelay(address: string, init: RequestInit = {}): Promise<Response> {
	const tls = relayTLS();
	const dial = relayDialOf(address);
	const dialled = dial.host ? { ...init, headers: { ...headersRecordOf(init.headers), Host: dial.host } } : init;
	return tls ? fetch(dial.target, { ...dialled, tls }) : fetch(dial.target, dialled);
}

function headersRecordOf(headers: HeadersInit | undefined): Record<string, string> {
	const record: Record<string, string> = {};
	new Headers(headers).forEach((value, name) => {
		record[name] = value;
	});
	return record;
}
