import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fetchFromRelay, openRelaySocket, relayCertificatePathVariable, relayTLS } from "../src/adapters/buzz/relay-trust.ts";

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
