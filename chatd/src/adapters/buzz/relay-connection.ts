import { matchFilter, type Filter } from "nostr-tools/filter";
import { openRelaySocket } from "./relay-trust.ts";
import type { BuzzEvent } from "./types.ts";
import { verifiedEvent } from "./verified-event.ts";

type EventListener = (event: BuzzEvent) => void;

export type LiveSubscription = {
	askAgain: () => void;
	close: () => void;
};

export type RelayConnection = {
	pubkeyHex: string;
	connect: () => Promise<void>;
	disconnect: () => void;
	subscribe: (filtersNow: () => object[], onEvent: EventListener) => LiveSubscription;
	query: (filter: object, timeoutMs?: number) => Promise<BuzzEvent[]>;
	queryComplete: (filter: object, timeoutMs?: number) => Promise<{ events: BuzzEvent[]; complete: boolean }>;
	publish: (kind: number, content: string, tags: string[][]) => Promise<BuzzEvent>;
	publishForAcknowledgement: (kind: number, content: string, tags: string[][]) => Promise<string>;
};

const authGraceMilliseconds = 3_000;
// The Buzz relay refuses a REQ carrying more filters than this (MAX_FILTERS_PER_REQ
// in crates/buzz-relay/src/protocol.rs, advertised as NIP-11 max_filters).
const filtersPerRequest = 10;

export class RelayQueryIncomplete extends Error {
	constructor(readonly relayURL: string, readonly reason: string) {
		super(`${relayURL} did not answer a query in full: ${reason}`);
		this.name = "RelayQueryIncomplete";
	}
}

export async function profileEventsOrNone(
	relay: { query: (filter: object) => Promise<BuzzEvent[]> },
	filter: object,
): Promise<BuzzEvent[] | null> {
	try {
		return await relay.query(filter);
	} catch (thrown) {
		if (thrown instanceof RelayQueryIncomplete) return null;
		throw thrown;
	}
}

function authorsThatAreNotPublicKeys(filter: object): unknown[] {
	const authors: unknown = "authors" in filter ? filter.authors : [];
	if (!Array.isArray(authors)) return [authors];
	return authors.filter((author) => typeof author !== "string" || !/^[0-9a-f]{64}$/.test(author));
}

type QueryAnswer = { events: BuzzEvent[]; complete: boolean; reason: string; isTimedOut: boolean };

type PendingAsk = {
	filter: object;
	events: BuzzEvent[];
	requestID: string;
	timeoutHandle: ReturnType<typeof setTimeout>;
	settle: (answer: QueryAnswer) => void;
};

type PendingRequest = { asks: PendingAsk[]; isAskedAgain: boolean };

function retryDelayStatedIn(reason: string, shortestMilliseconds: number): number {
	const stated = /retry in (\d+)s/.exec(reason);
	return Math.max(stated ? Number(stated[1]) * 1_000 : 0, shortestMilliseconds);
}

// Bun's WebSocket can ping; the DOM type it is declared with cannot say so.
function pingFunctionOf(socket: WebSocket): (() => void) | undefined {
	const ping: unknown = Reflect.get(socket, "ping");
	if (typeof ping !== "function") return undefined;
	return () => Reflect.apply(ping, socket, []);
}

// Subscriptions a relay refused together come back together unless each waits
// a little more or less than the rest.
function jitterMilliseconds(backoffMilliseconds: number): number {
	return Math.floor(Math.random() * backoffMilliseconds * 0.5);
}

function answers(ask: PendingAsk, event: BuzzEvent): boolean {
	return matchFilter(ask.filter as Filter, event) && !ask.events.some((held) => held.id === event.id);
}

function withinLimit(filter: object, events: BuzzEvent[]): BuzzEvent[] {
	const limit = "limit" in filter && typeof filter.limit === "number" ? filter.limit : events.length;
	if (events.length <= limit) return events;
	const newest = new Set(
		[...events].sort((first, second) => second.created_at - first.created_at).slice(0, limit).map((event) => event.id),
	);
	return events.filter((event) => newest.has(event.id));
}

export type RelayClientTiming = {
	resubscribeDelayMilliseconds: number;
	loginRetryDelayMilliseconds: number;
	livenessProbeIntervalMilliseconds: number;
};

const defaultTiming: RelayClientTiming = {
	resubscribeDelayMilliseconds: 1_000,
	loginRetryDelayMilliseconds: 1_000,
	livenessProbeIntervalMilliseconds: 30_000,
};

const maximumResubscribeDelayMilliseconds = 30_000;
const maximumLoginRetryDelayMilliseconds = 30_000;

export type EventSigner = {
	pubkeyHex: string;
	signEvent: (kind: number, content: string, tags: string[][]) => BuzzEvent;
};

function describeEventID(value: unknown): string {
	if (typeof value === "object" && value !== null && "id" in value && typeof value.id === "string") return value.id;
	return "with no id";
}

export function createRelayConnection(
	relayURL: string,
	signer: EventSigner,
	authTagJSON?: string,
	timing: RelayClientTiming = defaultTiming,
): RelayConnection {
	const { pubkeyHex, signEvent } = signer;

	let websocket: WebSocket | null = null;
	let isAuthed = false;
	let loginRefusal: Error | null = null;
	let reconnectDelayMs = 1_000;
	let shouldReconnect = true;
	let subscriptionSerial = 0;
	let livenessProbeTimer: ReturnType<typeof setInterval> | null = null;
	let isAwaitingPong = false;
	let areLiveSubscriptionsAsked = false;
	const liveSubscriptions = new Map<string, { filtersNow: () => object[]; onEvent: EventListener }>();
	const resubscribeDelays = new Map<string, number>();
	const pendingRequests = new Map<string, PendingRequest>();
	let forming: PendingAsk[] = [];
	const pendingPublishes = new Map<string, { resolve: (message: string) => void; reject: (error: Error) => void }>();
	let openWaiters: Array<() => void> = [];
	let authWaiters: Array<(reason?: Error) => void> = [];

	const isDebugEnabled = Bun.env.BUZZ_RELAY_DEBUG === "1";

	function send(frame: unknown[]): void {
		if (isDebugEnabled) console.error("[buzz-relay] send", JSON.stringify(frame).slice(0, 160));
		websocket?.send(JSON.stringify(frame));
	}

	function openSocket(): void {
		const socket = openRelaySocket(relayURL);
		websocket = socket;
		socket.addEventListener("pong", () => {
			isAwaitingPong = false;
		});
		socket.onopen = () => {
			reconnectDelayMs = 1_000;
			isAuthed = false;
			isAwaitingPong = false;
			areLiveSubscriptionsAsked = false;
			loginRefusal = null;
			void askLiveSubscriptionsOnceAdmitted(socket);
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
			const reopen = setTimeout(() => {
				if (shouldReconnect) openSocket();
			}, reconnectDelayMs);
			reopen.unref?.();
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
					loginRefusal = null;
					for (const waiter of authWaiters) waiter();
					authWaiters = [];
				},
				reject: (reason) => {
					isAuthed = false;
					loginRefusal = reason;
					for (const waiter of authWaiters) waiter(reason);
					authWaiters = [];
				},
			});
			send(["AUTH", authEvent]);
			return;
		}
		if (frameType === "EVENT" && typeof rest[0] === "string") {
			const subscriptionID = rest[0];
			const event = verifiedEvent(rest[1]);
			if (!event) {
				console.error(`[buzz-relay] dropped event ${describeEventID(rest[1])} from ${relayURL}: its id or signature does not verify`);
				return;
			}
			for (const ask of pendingRequests.get(subscriptionID)?.asks ?? []) {
				if (answers(ask, event)) ask.events.push(event);
			}
			liveSubscriptions.get(subscriptionID)?.onEvent(event);
			return;
		}
		if (frameType === "EOSE" && typeof rest[0] === "string") {
			resubscribeDelays.delete(rest[0]);
			const request = pendingRequests.get(rest[0]);
			if (request) {
				pendingRequests.delete(rest[0]);
				send(["CLOSE", rest[0]]);
				for (const ask of request.asks) {
					settleAsk(ask, { events: withinLimit(ask.filter, ask.events), complete: true, reason: "", isTimedOut: false });
				}
			}
			return;
		}
		if (frameType === "CLOSED" && typeof rest[0] === "string") {
			handleClosedSubscription(rest[0], typeof rest[1] === "string" ? rest[1] : "");
			return;
		}
		if (frameType === "NOTICE") {
			console.error(`[buzz-relay] notice from ${relayURL}: ${String(rest[0] ?? "")}`);
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

	// A relay that drops a subscription says so with CLOSED and nothing more: no
	// event on it arrives again until it is requested again. The relay does this
	// when its handler pool is full, so the next request waits a little longer
	// each time it is refused.
	function handleClosedSubscription(subscriptionID: string, reason: string): void {
		const request = pendingRequests.get(subscriptionID);
		if (request) {
			if (!request.isAskedAgain && reason.startsWith("auth-required")) {
				void requestAgainAfterLogin(subscriptionID, request);
				return;
			}
			if (!request.isAskedAgain && reason.startsWith("rate-limited:")) {
				requestAgainAfter(subscriptionID, request, retryDelayStatedIn(reason, timing.resubscribeDelayMilliseconds));
				return;
			}
			pendingRequests.delete(subscriptionID);
			console.error(`[buzz-relay] ${relayURL} closed query ${subscriptionID}: ${reason}`);
			for (const ask of request.asks) settleAsk(ask, { events: ask.events, complete: false, reason, isTimedOut: false });
			return;
		}
		if (!liveSubscriptions.has(subscriptionID)) return;
		const backoff = resubscribeDelays.get(subscriptionID) ?? timing.resubscribeDelayMilliseconds;
		const delay = retryDelayStatedIn(reason, backoff) + jitterMilliseconds(backoff);
		console.error(`[buzz-relay] ${relayURL} closed subscription ${subscriptionID}: ${reason}; requesting it again in ${delay}ms`);
		resubscribeDelays.set(subscriptionID, Math.min(backoff * 2, maximumResubscribeDelayMilliseconds));
		const retry = setTimeout(() => void requestSubscriptionAgain(subscriptionID, reason), delay);
		retry.unref?.();
	}

	async function requestAgainAfterLogin(subscriptionID: string, request: PendingRequest): Promise<void> {
		request.isAskedAgain = true;
		await waitForAuth().catch(() => void 0);
		sendRequestAgain(subscriptionID, request);
	}

	function requestAgainAfter(subscriptionID: string, request: PendingRequest, delayMilliseconds: number): void {
		request.isAskedAgain = true;
		const retry = setTimeout(() => sendRequestAgain(subscriptionID, request), delayMilliseconds);
		retry.unref?.();
	}

	function sendRequestAgain(subscriptionID: string, request: PendingRequest): void {
		if (pendingRequests.get(subscriptionID) !== request) return;
		for (const ask of request.asks) ask.events = [];
		send(["REQ", subscriptionID, ...request.asks.map((ask) => ask.filter)]);
	}

	function settleAsk(ask: PendingAsk, answer: QueryAnswer): void {
		clearTimeout(ask.timeoutHandle);
		ask.settle(answer);
	}

	function giveUpOn(ask: PendingAsk, reason: string): void {
		forming = forming.filter((formingAsk) => formingAsk !== ask);
		const request = pendingRequests.get(ask.requestID);
		if (request) {
			request.asks = request.asks.filter((asked) => asked !== ask);
			if (request.asks.length === 0) {
				pendingRequests.delete(ask.requestID);
				send(["CLOSE", ask.requestID]);
			}
		}
		ask.settle({ events: ask.events, complete: false, reason, isTimedOut: true });
	}

	// Reads asked for in the same moment leave as one REQ, because the relay
	// counts frames against a person's quota and a screen asks for many at once.
	function sendWhatIsForming(): void {
		const asks = forming;
		forming = [];
		for (let start = 0; start < asks.length; start += filtersPerRequest) {
			sendRequest(`query-${subscriptionSerial++}`, asks.slice(start, start + filtersPerRequest));
		}
	}

	function sendRequest(requestID: string, asks: PendingAsk[]): void {
		for (const ask of asks) ask.requestID = requestID;
		pendingRequests.set(requestID, { asks, isAskedAgain: false });
		send(["REQ", requestID, ...asks.map((ask) => ask.filter)]);
	}

	function pendingAsk(filter: object, timeoutMs: number, settle: (answer: QueryAnswer) => void): PendingAsk {
		const pending: PendingAsk = {
			filter,
			events: [],
			requestID: "",
			timeoutHandle: setTimeout(() => giveUpOn(pending, `no answer within ${timeoutMs}ms`), timeoutMs),
			settle,
		};
		return pending;
	}

	async function requestSubscriptionAgain(subscriptionID: string, reason: string): Promise<void> {
		if (websocket?.readyState !== WebSocket.OPEN) return;
		if (reason.startsWith("auth-required")) await waitForAuth().catch(() => void 0);
		askLive(subscriptionID);
	}

	// A subscription asked before the login is refused as auth-required, so a
	// fresh socket asks for each one once, after the relay has let it in.
	async function askLiveSubscriptionsOnceAdmitted(socket: WebSocket): Promise<void> {
		const isAdmitted = await waitForAuth().then(
			() => true,
			() => false,
		);
		if (!isAdmitted || websocket !== socket || socket.readyState !== WebSocket.OPEN) return;
		areLiveSubscriptionsAsked = true;
		for (const subscriptionID of liveSubscriptions.keys()) askLive(subscriptionID);
	}

	function askLive(subscriptionID: string): void {
		const subscription = liveSubscriptions.get(subscriptionID);
		if (!subscription || !areLiveSubscriptionsAsked || websocket?.readyState !== WebSocket.OPEN) return;
		send(["REQ", subscriptionID, ...subscription.filtersNow()]);
	}

	// Nothing in the protocol tells a client that a silent socket is a dead one.
	// A ping the relay has not answered by the next one is that signal, and
	// closing the socket is what brings the reconnect, with every subscription.
	// A ping is a control frame, so it costs nothing against the relay's quota.
	function probeLiveness(): void {
		const socket = websocket;
		if (socket?.readyState !== WebSocket.OPEN) return;
		if (isAwaitingPong) {
			console.error(`[buzz-relay] ${relayURL} did not answer a ping in ${timing.livenessProbeIntervalMilliseconds}ms; reconnecting`);
			socket.close();
			return;
		}
		isAwaitingPong = true;
		pingFunctionOf(socket)?.();
	}

	function startLivenessProbes(): void {
		if (livenessProbeTimer) return;
		livenessProbeTimer = setInterval(() => void probeLiveness(), timing.livenessProbeIntervalMilliseconds);
		livenessProbeTimer.unref?.();
	}

	function stopLivenessProbes(): void {
		if (livenessProbeTimer) clearInterval(livenessProbeTimer);
		livenessProbeTimer = null;
	}

	// A relay that never challenges is one that does not require auth, and a wait
	// with no end would hold every message behind a handshake that is not coming.
	async function waitForAuth(): Promise<void> {
		if (isAuthed) return;
		if (loginRefusal) throw loginRefusal;
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

	async function attemptConnection(): Promise<void> {
		openSocket();
		await waitForOpen();
		await waitForAuth();
	}

	function abandonSocket(): void {
		const abandoned = websocket;
		if (!abandoned) return;
		abandoned.onclose = null;
		abandoned.close();
		websocket = null;
	}

	function pause(milliseconds: number): Promise<void> {
		return new Promise((resolve) => setTimeout(resolve, milliseconds));
	}

	async function connectUntilAdmitted(): Promise<void> {
		let delay = timing.loginRetryDelayMilliseconds;
		while (shouldReconnect) {
			try {
				await attemptConnection();
				return;
			} catch (refusal) {
				const reason = refusal instanceof Error ? refusal.message : String(refusal);
				console.error(`[buzz-relay] ${relayURL} refused the login (${reason}); trying again in ${delay}ms`);
				abandonSocket();
				await pause(delay);
				delay = Math.min(delay * 2, maximumLoginRetryDelayMilliseconds);
			}
		}
	}

	async function waitForOpen(): Promise<void> {
		if (websocket?.readyState === WebSocket.OPEN) return;
		await new Promise<void>((resolve) => openWaiters.push(resolve));
	}

	return {
		pubkeyHex,
		async connect() {
			shouldReconnect = true;
			await connectUntilAdmitted();
			if (shouldReconnect) startLivenessProbes();
		},
		disconnect() {
			shouldReconnect = false;
			stopLivenessProbes();
			websocket?.close();
		},
		subscribe(filtersNow, onEvent) {
			const subscriptionID = `live-${subscriptionSerial++}`;
			liveSubscriptions.set(subscriptionID, { filtersNow, onEvent });
			askLive(subscriptionID);
			return {
				askAgain: () => askLive(subscriptionID),
				close: () => {
					if (!liveSubscriptions.delete(subscriptionID)) return;
					resubscribeDelays.delete(subscriptionID);
					if (areLiveSubscriptionsAsked && websocket?.readyState === WebSocket.OPEN) send(["CLOSE", subscriptionID]);
				},
			};
		},
		async query(filter, timeoutMs = 8_000) {
			const answer = await ask(filter, timeoutMs);
			if (!answer.complete) throw new RelayQueryIncomplete(relayURL, answer.reason);
			return answer.events;
		},
		async queryComplete(filter, timeoutMs = 8_000) {
			const { events, complete } = await ask(filter, timeoutMs);
			return { events, complete };
		},
		async publish(kind, content, tags) {
			return (await publishAndAwaitAcknowledgement(kind, content, tags)).event;
		},
		async publishForAcknowledgement(kind, content, tags) {
			return (await publishAndAwaitAcknowledgement(kind, content, tags)).acknowledgement;
		},
	};

	async function ask(filter: object, timeoutMs: number): Promise<QueryAnswer> {
		const notPublicKeys = authorsThatAreNotPublicKeys(filter);
		if (notPublicKeys.length > 0) {
			const reason = `authors holds what is not a public key: ${JSON.stringify(notPublicKeys)}`;
			console.error(`[buzz-relay] not asking ${relayURL}: ${reason}`);
			return { events: [], complete: false, reason, isTimedOut: false };
		}
		await waitForOpen();
		return await new Promise<QueryAnswer>((resolve) => {
			if (forming.length === 0) queueMicrotask(sendWhatIsForming);
			forming.push(pendingAsk(filter, timeoutMs, resolve));
		});
	}

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
