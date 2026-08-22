import { finalizeEvent, getPublicKey } from "nostr-tools/pure";
import type { BuzzEvent } from "./types.ts";

type EventListener = (event: BuzzEvent) => void;

export type BuzzRelayClient = {
	pubkeyHex: string;
	connect: () => Promise<void>;
	disconnect: () => void;
	subscribe: (filters: object[], onEvent: EventListener) => void;
	query: (filter: object, timeoutMs?: number) => Promise<BuzzEvent[]>;
	publish: (kind: number, content: string, tags: string[][]) => Promise<BuzzEvent>;
	publishForAcknowledgement: (kind: number, content: string, tags: string[][]) => Promise<string>;
};

const authGraceMilliseconds = 3_000;

export function createBuzzRelayClient(relayURL: string, privateKeyHex: string, authTagJSON?: string): BuzzRelayClient {
	const secretKey = hexToBytes(privateKeyHex);
	const pubkeyHex = getPublicKey(secretKey);

	let websocket: WebSocket | null = null;
	let isAuthed = false;
	let reconnectDelayMs = 1_000;
	let shouldReconnect = true;
	let subscriptionSerial = 0;
	const liveSubscriptions = new Map<string, { filters: object[]; onEvent: EventListener }>();
	const pendingQueries = new Map<string, { events: BuzzEvent[]; resolve: (events: BuzzEvent[]) => void }>();
	const pendingPublishes = new Map<string, { resolve: (message: string) => void; reject: (error: Error) => void }>();
	let openWaiters: Array<() => void> = [];
	let authWaiters: Array<(reason?: Error) => void> = [];

	function signEvent(kind: number, content: string, tags: string[][]): BuzzEvent {
		return finalizeEvent(
			{ kind, content, tags, created_at: Math.floor(Date.now() / 1000) },
			secretKey,
		) as BuzzEvent;
	}

	const isDebugEnabled = Bun.env.BUZZ_RELAY_DEBUG === "1";

	function send(frame: unknown[]): void {
		if (isDebugEnabled) console.error("[buzz-relay] send", JSON.stringify(frame).slice(0, 160));
		websocket?.send(JSON.stringify(frame));
	}

	function openSocket(): void {
		websocket = new WebSocket(relayURL);
		websocket.onopen = () => {
			reconnectDelayMs = 1_000;
			isAuthed = false;
			for (const [subscriptionID, subscription] of liveSubscriptions) {
				send(["REQ", subscriptionID, ...subscription.filters]);
			}
			for (const waiter of openWaiters) waiter();
			openWaiters = [];
		};
		websocket.onmessage = (message) => {
			let frame: unknown[];
			try {
				frame = JSON.parse(String(message.data));
			} catch {
				return;
			}
			handleFrame(frame);
		};
		websocket.onclose = () => {
			if (!shouldReconnect) return;
			setTimeout(openSocket, reconnectDelayMs);
			reconnectDelayMs = Math.min(reconnectDelayMs * 2, 30_000);
		};
		websocket.onerror = () => {};
	}

	function handleFrame(frame: unknown[]): void {
		if (isDebugEnabled) console.error("[buzz-relay] recv", JSON.stringify(frame).slice(0, 160));
		const [frameType, ...rest] = frame;
		if (frameType === "AUTH" && typeof rest[0] === "string") {
			const challenge = rest[0];
			const authTags = [
				["relay", relayURL],
				["challenge", challenge],
			];
			if (authTagJSON) {
				try {
					authTags.push(JSON.parse(authTagJSON) as string[]);
				} catch {
					void 0;
				}
			}
			const authEvent = signEvent(22242, "", authTags);
			// The relay answers an AUTH with an OK carrying its id, the same way it
			// answers a publish. Having written the frame is not the same as having
			// been let in.
			pendingPublishes.set(authEvent.id, {
				resolve: () => {
					isAuthed = true;
					for (const waiter of authWaiters) waiter();
					authWaiters = [];
				},
				reject: (reason) => {
					isAuthed = false;
					for (const waiter of authWaiters) waiter(reason);
					authWaiters = [];
				},
			});
			send(["AUTH", authEvent]);
			for (const [subscriptionID, subscription] of liveSubscriptions) {
				send(["REQ", subscriptionID, ...subscription.filters]);
			}
			return;
		}
		if (frameType === "EVENT" && typeof rest[0] === "string") {
			const subscriptionID = rest[0];
			const event = rest[1] as BuzzEvent;
			pendingQueries.get(subscriptionID)?.events.push(event);
			liveSubscriptions.get(subscriptionID)?.onEvent(event);
			return;
		}
		if (frameType === "EOSE" && typeof rest[0] === "string") {
			const query = pendingQueries.get(rest[0]);
			if (query) {
				pendingQueries.delete(rest[0]);
				send(["CLOSE", rest[0]]);
				query.resolve(query.events);
			}
			return;
		}
		if (frameType === "OK" && typeof rest[0] === "string") {
			const publishWaiter = pendingPublishes.get(rest[0]);
			if (!publishWaiter) return;
			pendingPublishes.delete(rest[0]);
			if (rest[1] === true) publishWaiter.resolve(typeof rest[2] === "string" ? rest[2] : "");
			else publishWaiter.reject(new Error(`relay rejected event: ${String(rest[2] ?? "")}`));
		}
	}

	// A relay that never challenges is one that does not require auth, and a wait
	// with no end would hold every message behind a handshake that is not coming.
	async function waitForAuth(): Promise<void> {
		if (isAuthed) return;
		await new Promise<void>((resolve, reject) => {
			const settle = setTimeout(resolve, authGraceMilliseconds);
			settle.unref?.();
			authWaiters.push((reason) => {
				clearTimeout(settle);
				if (reason) reject(reason);
				else resolve();
			});
		});
	}

	async function waitForOpen(): Promise<void> {
		if (websocket?.readyState === WebSocket.OPEN) return;
		await new Promise<void>((resolve) => openWaiters.push(resolve));
	}

	return {
		pubkeyHex,
		async connect() {
			shouldReconnect = true;
			openSocket();
			await waitForOpen();
			await waitForAuth();
		},
		disconnect() {
			shouldReconnect = false;
			websocket?.close();
		},
		subscribe(filters, onEvent) {
			const subscriptionID = `live-${subscriptionSerial++}`;
			liveSubscriptions.set(subscriptionID, { filters, onEvent });
			if (websocket?.readyState === WebSocket.OPEN) {
				send(["REQ", subscriptionID, ...filters]);
			}
		},
		async query(filter, timeoutMs = 8_000) {
			await waitForOpen();
			const subscriptionID = `query-${subscriptionSerial++}`;
			return await new Promise<BuzzEvent[]>((resolve) => {
				const timeoutHandle = setTimeout(() => {
					const query = pendingQueries.get(subscriptionID);
					pendingQueries.delete(subscriptionID);
					send(["CLOSE", subscriptionID]);
					resolve(query?.events ?? []);
				}, timeoutMs);
				pendingQueries.set(subscriptionID, {
					events: [],
					resolve: (events) => {
						clearTimeout(timeoutHandle);
						resolve(events);
					},
				});
				send(["REQ", subscriptionID, filter]);
			});
		},
		async publish(kind, content, tags) {
			return (await publishAndAwaitAcknowledgement(kind, content, tags)).event;
		},
		async publishForAcknowledgement(kind, content, tags) {
			return (await publishAndAwaitAcknowledgement(kind, content, tags)).acknowledgement;
		},
	};

	async function publishAndAwaitAcknowledgement(
		kind: number,
		content: string,
		tags: string[][],
	): Promise<{ event: BuzzEvent; acknowledgement: string }> {
		await waitForOpen();
		const event = signEvent(kind, content, tags);
		const acknowledgement = await new Promise<string>((resolve, reject) => {
			const timeoutHandle = setTimeout(() => {
				pendingPublishes.delete(event.id);
				reject(new Error("relay publish timed out"));
			}, 8_000);
			pendingPublishes.set(event.id, {
				resolve: (message) => {
					clearTimeout(timeoutHandle);
					resolve(message);
				},
				reject: (error) => {
					clearTimeout(timeoutHandle);
					reject(error);
				},
			});
			send(["EVENT", event]);
		});
		return { event, acknowledgement };
	}
}

function hexToBytes(hex: string): Uint8Array {
	const bytes = new Uint8Array(hex.length / 2);
	for (let index = 0; index < bytes.length; index++) {
		bytes[index] = Number.parseInt(hex.slice(index * 2, index * 2 + 2), 16);
	}
	return bytes;
}
