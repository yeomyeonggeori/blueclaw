import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fetchFromRelay, openRelaySocket, relayCertificatePathVariable, relayDialOf, relayTLS, relayURLOf } from "../src/adapters/buzz/relay-trust.ts";

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
});

describe("a relay reached under a public name", () => {
	const environment = {
		CHATD_BUZZ_RELAY_URL: "wss://acme.example.test",
		CHATD_BUZZ_RELAY_ADDRESS: "ws://127.0.0.1:3000",
	};

	test("is dialled on loopback and told the name it answers to", () => {
		expect(relayDialOf("wss://acme.example.test", environment)).toEqual({
			target: "ws://127.0.0.1:3000/",
			host: "acme.example.test",
		});
		expect(relayDialOf("https://acme.example.test/media/abc.png?x=1", environment)).toEqual({
			target: "http://127.0.0.1:3000/media/abc.png?x=1",
			host: "acme.example.test",
		});
	});

	test("leaves an address on any other host alone", () => {
		expect(relayDialOf("https://elsewhere.example.test/media/abc.png", environment)).toEqual({
			target: "https://elsewhere.example.test/media/abc.png",
			host: undefined,
		});
	});

	test("dials what it is told when no address is configured", () => {
		expect(relayDialOf("ws://127.0.0.1:3000", { CHATD_BUZZ_RELAY_URL: "ws://127.0.0.1:3000" })).toEqual({
			target: "ws://127.0.0.1:3000",
			host: undefined,
		});
	});

	test("takes the messenger's own address when chatd is given none", () => {
		expect(relayURLOf({ RELAY_URL: "wss://acme.example.test" })).toBe("wss://acme.example.test");
		expect(relayURLOf({ CHATD_BUZZ_RELAY_URL: "ws://127.0.0.1:3000", RELAY_URL: "wss://acme.example.test" })).toBe(
			"ws://127.0.0.1:3000",
		);
	});

	test("reaches a real relay on loopback under the public name", async () => {
		const seen: (string | null)[] = [];
		const relay = Bun.serve({
			port: 0,
			fetch(request, server) {
				seen.push(request.headers.get("host"));
				if (server.upgrade(request)) return;
				return new Response("ok");
			},
			websocket: {
				open(socket) {
					socket.send("hello");
				},
				message() {},
			},
		});
		const runtime = Bun.env;
		runtime.CHATD_BUZZ_RELAY_URL = "wss://acme.example.test";
		runtime.CHATD_BUZZ_RELAY_ADDRESS = `ws://127.0.0.1:${relay.port}`;
		try {
			expect(await (await fetchFromRelay("https://acme.example.test/info")).text()).toBe("ok");
			const socket = openRelaySocket("wss://acme.example.test");
			const greeting = await new Promise<string>((settle) =>
				socket.addEventListener("message", (message) => settle(String(message.data))),
			);
			socket.close();
			expect(greeting).toBe("hello");
			expect(seen).toEqual(["acme.example.test", "acme.example.test"]);
		} finally {
			delete runtime.CHATD_BUZZ_RELAY_URL;
			delete runtime.CHATD_BUZZ_RELAY_ADDRESS;
			relay.stop(true);
		}
	});
});
