import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { finalizeEvent, getPublicKey } from "nostr-tools/pure";
import { createRelayConnection, type RelayClientTiming } from "../src/adapters/buzz/relay-connection.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const secretKey = Uint8Array.from({ length: 32 }, () => 2);
const signer = {
	pubkeyHex: getPublicKey(secretKey),
	signEvent: (kind: number, content: string, tags: string[][]) =>
		finalizeEvent({ kind, content, tags, created_at: Math.floor(Date.now() / 1000) }, secretKey) as BuzzEvent,
};
const timing: RelayClientTiming = {
	resubscribeDelayMilliseconds: 5,
	loginRetryDelayMilliseconds: 5,
	livenessProbeIntervalMilliseconds: 60_000,
	livenessProbeTimeoutMilliseconds: 60_000,
};
const refusalReason = "restricted: not a relay member";

type FakeRelay = { url: string; authAttempts: number; connections: number; stop: () => void };

function startRelayAdmittingOnAttempt(admittedAttempt: number): FakeRelay {
	const relay = { url: "", authAttempts: 0, connections: 0, stop: () => void 0 };
	const server = Bun.serve({
		port: 0,
		fetch(request, serverInstance) {
			if (serverInstance.upgrade(request)) return undefined;
			return new Response("expected a websocket", { status: 400 });
		},
		websocket: {
			open(socket) {
				relay.connections += 1;
				socket.send(JSON.stringify(["AUTH", "challenge"]));
			},
			message(socket, data) {
				const frame = JSON.parse(String(data)) as unknown[];
				if (frame[0] !== "AUTH") return;
				relay.authAttempts += 1;
				const isAdmitted = relay.authAttempts >= admittedAttempt;
				const eventID = (frame[1] as { id: string }).id;
				socket.send(JSON.stringify(["OK", eventID, isAdmitted, isAdmitted ? "" : refusalReason]));
			},
		},
	});
	relay.url = `ws://localhost:${server.port}`;
	relay.stop = () => void server.stop(true);
	return relay;
}

const realConsoleError = console.error;
let loggedLines: string[] = [];
let relay: FakeRelay;

beforeEach(() => {
	loggedLines = [];
	console.error = (line: unknown) => void loggedLines.push(String(line));
});

afterEach(() => {
	console.error = realConsoleError;
	relay.stop();
});

describe("relay connection login retry", () => {
	test("connect retries a refused login until the relay lets the agent in", async () => {
		relay = startRelayAdmittingOnAttempt(3);
		const connection = createRelayConnection(relay.url, signer, undefined, timing);

		await connection.connect();
		connection.disconnect();

		expect(relay.authAttempts).toBe(3);
		expect(relay.connections).toBe(3);
		const refusals = loggedLines.filter((line) => line.includes(refusalReason));
		expect(refusals).toHaveLength(2);
		expect(refusals[0]).toContain(relay.url);
	});

	test("connect does not retry when the first login is accepted", async () => {
		relay = startRelayAdmittingOnAttempt(1);
		const connection = createRelayConnection(relay.url, signer, undefined, timing);

		await connection.connect();
		connection.disconnect();

		expect(relay.connections).toBe(1);
		expect(loggedLines).toEqual([]);
	});

	test("a refusal delivered before anything waits for login is not mistaken for admission", async () => {
		const realWebSocket = globalThis.WebSocket;
		let openedSockets = 0;
		class RefusingOnceSocket {
			static OPEN = 1;
			readyState = 0;
			onopen: (() => void) | null = null;
			onmessage: ((message: { data: string }) => void) | null = null;
			onclose: (() => void) | null = null;
			onerror: (() => void) | null = null;
			private readonly isRefusing = ++openedSockets === 1;

			constructor(_url: string) {
				queueMicrotask(() => {
					this.readyState = 1;
					this.onopen?.();
					this.receive(["AUTH", "challenge"]);
				});
			}

			send(text: string): void {
				const frame = JSON.parse(text) as unknown[];
				if (frame[0] !== "AUTH") return;
				const eventID = (frame[1] as { id: string }).id;
				this.receive(["OK", eventID, !this.isRefusing, this.isRefusing ? refusalReason : ""]);
			}

			close(): void {
				this.readyState = 3;
			}

			private receive(frame: unknown[]): void {
				this.onmessage?.({ data: JSON.stringify(frame) });
			}
		}
		(globalThis as { WebSocket: unknown }).WebSocket = RefusingOnceSocket;
		relay = { url: "", authAttempts: 0, connections: 0, stop: () => void 0 };
		const connection = createRelayConnection("wss://relay.example.test", signer, undefined, timing);
		const started = performance.now();

		try {
			await connection.connect();
		} finally {
			connection.disconnect();
			globalThis.WebSocket = realWebSocket;
		}

		expect(openedSockets).toBe(2);
		expect(performance.now() - started).toBeLessThan(2_500);
	});
});
