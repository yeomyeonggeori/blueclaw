import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { finalizeEvent, getPublicKey } from "nostr-tools/pure";
import { createRelayConnection, type RelayClientTiming } from "../src/adapters/buzz/relay-connection.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const secretKey = Uint8Array.from({ length: 32 }, () => 1);
const signer = {
	pubkeyHex: getPublicKey(secretKey),
	signEvent: (kind: number, content: string, tags: string[][]) =>
		finalizeEvent({ kind, content, tags, created_at: Math.floor(Date.now() / 1000) }, secretKey) as BuzzEvent,
};
const relayURL = "wss://relay.example.test";
const timing: RelayClientTiming = {
	resubscribeDelayMilliseconds: 5,
	livenessProbeIntervalMilliseconds: 10,
	livenessProbeTimeoutMilliseconds: 10,
};

type Frame = unknown[];

class FakeSocket {
	static opened: FakeSocket[] = [];
	static answersProbes = true;
	readonly sent: Frame[] = [];
	readyState = 0;
	onopen: (() => void) | null = null;
	onmessage: ((message: { data: string }) => void) | null = null;
	onclose: (() => void) | null = null;
	onerror: (() => void) | null = null;

	constructor(_url: string) {
		FakeSocket.opened.push(this);
		queueMicrotask(() => {
			this.readyState = 1;
			this.onopen?.();
			this.receive(["AUTH", "challenge-1"]);
		});
	}

	send(text: string): void {
		const frame = JSON.parse(text) as Frame;
		this.sent.push(frame);
		if (frame[0] === "AUTH") this.receive(["OK", (frame[1] as { id: string }).id, true, ""]);
		if (frame[0] === "REQ" && String(frame[1]).startsWith("probe-") && FakeSocket.answersProbes) {
			this.receive(["EOSE", frame[1]]);
		}
	}

	close(): void {
		this.readyState = 3;
		this.onclose?.();
	}

	receive(frame: Frame): void {
		this.onmessage?.({ data: JSON.stringify(frame) });
	}

	requests(): Frame[] {
		return this.sent.filter((frame) => frame[0] === "REQ");
	}
}

const realWebSocket = globalThis.WebSocket;
const realConsoleError = console.error;

beforeEach(() => {
	FakeSocket.opened = [];
	FakeSocket.answersProbes = true;
	(globalThis as { WebSocket: unknown }).WebSocket = Object.assign(FakeSocket, { OPEN: 1 });
	console.error = () => void 0;
});

afterEach(() => {
	globalThis.WebSocket = realWebSocket;
	console.error = realConsoleError;
});

function settle(milliseconds = 0): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

describe("a subscription the relay closes", () => {
	test("is requested again rather than left dead", async () => {
		const client = createRelayConnection(relayURL, signer, undefined, timing);
		await client.connect();
		const socket = FakeSocket.opened[0]!;
		client.subscribe([{ kinds: [9], "#h": ["room-1"] }], () => void 0);
		const subscriptionID = socket.requests().at(-1)?.[1];

		const before = socket.requests().filter((frame) => frame[1] === subscriptionID).length;
		socket.receive(["CLOSED", subscriptionID, "rate-limited: too many concurrent requests"]);
		await settle(timing.resubscribeDelayMilliseconds * 3);

		expect(socket.requests().filter((frame) => frame[1] === subscriptionID)).toHaveLength(before + 1);
		client.disconnect();
	});

	test("waits longer each time the relay refuses it again", async () => {
		const client = createRelayConnection(relayURL, signer, undefined, timing);
		await client.connect();
		const socket = FakeSocket.opened[0]!;
		client.subscribe([{ kinds: [9] }], () => void 0);
		const subscriptionID = socket.requests().at(-1)?.[1];

		const before = socket.requests().filter((frame) => frame[1] === subscriptionID).length;
		socket.receive(["CLOSED", subscriptionID, "rate-limited"]);
		await settle(timing.resubscribeDelayMilliseconds * 3);
		socket.receive(["CLOSED", subscriptionID, "rate-limited"]);
		const rightAfterTheSecondRefusal = socket.requests().filter((frame) => frame[1] === subscriptionID).length;
		await settle(timing.resubscribeDelayMilliseconds * 4);

		expect(rightAfterTheSecondRefusal).toBe(before + 1);
		expect(socket.requests().filter((frame) => frame[1] === subscriptionID)).toHaveLength(before + 2);
		client.disconnect();
	});

	test("that was a query resolves with what arrived", async () => {
		const client = createRelayConnection(relayURL, signer, undefined, timing);
		await client.connect();
		const socket = FakeSocket.opened[0]!;
		const answer = client.query({ kinds: [0] });
		await settle();
		const queryID = socket.requests().at(-1)?.[1];

		socket.receive(["CLOSED", queryID, "error: database error"]);

		expect(await answer).toEqual([]);
		client.disconnect();
	});
});

describe("a relay that stops answering", () => {
	test("is reconnected, and every subscription requested again on the new socket", async () => {
		const client = createRelayConnection(relayURL, signer, undefined, timing);
		await client.connect();
		const first = FakeSocket.opened[0]!;
		client.subscribe([{ kinds: [9], "#h": ["room-1"] }], () => void 0);
		FakeSocket.answersProbes = false;

		await settle(timing.livenessProbeIntervalMilliseconds + timing.livenessProbeTimeoutMilliseconds + 5);
		FakeSocket.answersProbes = true;
		await settle(1_100);

		expect(first.readyState).toBe(3);
		expect(FakeSocket.opened.length).toBeGreaterThan(1);
		const second = FakeSocket.opened[1]!;
		expect(second.requests().some((frame) => JSON.stringify(frame).includes("room-1"))).toBe(true);
		client.disconnect();
	});

	test("is left alone while it answers probes", async () => {
		const client = createRelayConnection(relayURL, signer, undefined, timing);
		await client.connect();
		const socket = FakeSocket.opened[0]!;

		await settle(timing.livenessProbeIntervalMilliseconds * 4);

		expect(socket.requests().filter((frame) => String(frame[1]).startsWith("probe-")).length).toBeGreaterThan(1);
		expect(socket.readyState).toBe(1);
		expect(FakeSocket.opened).toHaveLength(1);
		client.disconnect();
	});
});
