import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	dialledAddressOf,
	fetchFromRelay,
	openRelaySocket,
	relayCertificatePathVariable,
	relayDialURLVariable,
	relayTLS,
} from "../src/adapters/buzz/relay-trust.ts";

const certificate = "-----BEGIN CERTIFICATE-----\nSAMPLE\n-----END CERTIFICATE-----\n";

function writeCertificate(): string {
	const path = join(mkdtempSync(join(tmpdir(), "relay-trust-")), "relay.pem");
	writeFileSync(path, certificate);
	return path;
}

describe("relay trust", () => {
	const realWebSocket = globalThis.WebSocket;
	const realFetch = globalThis.fetch;
	afterEach(() => {
		globalThis.WebSocket = realWebSocket;
		globalThis.fetch = realFetch;
		delete Bun.env[relayCertificatePathVariable];
		delete Bun.env[relayDialURLVariable];
		delete Bun.env.CHATD_BUZZ_RELAY_URL;
	});

	test("trusts nothing extra when no certificate is configured", () => {
		expect(relayTLS({})).toBeUndefined();
	});

	test("hands the configured certificate to the relay socket", () => {
		Bun.env[relayCertificatePathVariable] = writeCertificate();
		const seen: unknown[] = [];
		class RecordingSocket {
			constructor(_url: string, options?: unknown) {
				seen.push(options);
			}
		}
		(globalThis as { WebSocket: unknown }).WebSocket = RecordingSocket;
		openRelaySocket("wss://relay.example.test");
		expect(seen).toEqual([{ tls: { ca: certificate } }]);
	});

	test("hands the configured certificate to a relay fetch and to no other", async () => {
		Bun.env[relayCertificatePathVariable] = writeCertificate();
		const seen: unknown[] = [];
		(globalThis as { fetch: unknown }).fetch = async (_address: string, init?: { tls?: unknown }) => {
			seen.push(init?.tls);
			return new Response("");
		};
		await fetchFromRelay("https://relay.example.test/media");
		await fetch("https://other.example.test");
		expect(seen).toEqual([{ ca: certificate }, undefined]);
	});

	test("dials a relay at its public name where it is dialled, presenting the name it answers to", () => {
		const environment = { CHATD_BUZZ_RELAY_URL: "wss://acme.example.test", [relayDialURLVariable]: "ws://127.0.0.1:3000" };
		expect(dialledAddressOf("wss://acme.example.test", environment)).toEqual({
			address: "ws://127.0.0.1:3000/",
			host: "acme.example.test",
		});
		expect(dialledAddressOf("https://acme.example.test/media/abc.png?x=1", environment)).toEqual({
			address: "http://127.0.0.1:3000/media/abc.png?x=1",
			host: "acme.example.test",
		});
	});

	test("leaves an address elsewhere, or a relay with no dial address, as it is", () => {
		const environment = { CHATD_BUZZ_RELAY_URL: "wss://acme.example.test", [relayDialURLVariable]: "ws://127.0.0.1:3000" };
		expect(dialledAddressOf("https://store.example.test/object", environment)).toEqual({
			address: "https://store.example.test/object",
		});
		expect(dialledAddressOf("wss://acme.example.test", { CHATD_BUZZ_RELAY_URL: "wss://acme.example.test" })).toEqual({
			address: "wss://acme.example.test",
		});
	});

	test("a relay on loopback hears its public name on the socket and on a fetch", async () => {
		const heard: string[] = [];
		const relay = Bun.serve({
			port: 0,
			fetch(request, server) {
				heard.push(request.headers.get("host") ?? "");
				if (server.upgrade(request)) return undefined;
				return new Response("ok");
			},
			websocket: { open: (socket) => socket.close(), message: () => undefined },
		});
		Bun.env.CHATD_BUZZ_RELAY_URL = "wss://acme.example.test";
		Bun.env[relayDialURLVariable] = `ws://127.0.0.1:${relay.port}`;
		try {
			const socket = openRelaySocket("wss://acme.example.test");
			await new Promise((resolve) => socket.addEventListener("close", resolve));
			await fetchFromRelay("https://acme.example.test/media/abc.png", { headers: { Authorization: "Nostr x" } });
			expect(heard).toEqual(["acme.example.test", "acme.example.test"]);
		} finally {
			relay.stop(true);
		}
	});
});
