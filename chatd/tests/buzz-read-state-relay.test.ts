import { afterAll, beforeAll, describe, expect, mock, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey, getPublicKey } from "nostr-tools/pure";
import { bytesToHex, hexToBytes } from "nostr-tools/utils";
import { v2 as nip44 } from "nostr-tools/nip44";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import { createRelayConnection } from "../src/adapters/buzz/relay-connection.ts";
import { READ_STATE_KIND, readStateDTag } from "../src/adapters/buzz/read-state.ts";
import { markConversationReadAsUser, unreadCountsAsUser } from "../src/adapters/buzz/user-read-state.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

mock.module("../src/adapters/buzz/relay-client.ts", () => ({
	createBuzzRelayClient: (url: string, privateKeyHex: string) => {
		const secretKey = hexToBytes(privateKeyHex);
		return createRelayConnection(url, {
			pubkeyHex: getPublicKey(secretKey),
			signEvent: (kind: number, content: string, tags: string[][]) =>
				finalizeEvent({ kind, content, tags, created_at: Math.floor(Date.now() / 1000) }, secretKey) as BuzzEvent,
		});
	},
}));

type Filter = {
	kinds?: number[];
	authors?: string[];
	since?: number;
	until?: number;
	limit?: number;
} & Record<string, unknown>;

const stored: BuzzEvent[] = [];

function matches(event: BuzzEvent, filter: Filter): boolean {
	if (filter.kinds && !filter.kinds.includes(event.kind)) return false;
	if (filter.authors && !filter.authors.includes(event.pubkey)) return false;
	if (filter.since !== undefined && event.created_at < filter.since) return false;
	if (filter.until !== undefined && event.created_at > filter.until) return false;
	for (const [key, wanted] of Object.entries(filter)) {
		if (!key.startsWith("#") || !Array.isArray(wanted)) continue;
		const tagName = key.slice(1);
		if (!event.tags.some((tag) => tag[0] === tagName && wanted.includes(tag[1] ?? ""))) return false;
	}
	return true;
}

function keep(event: BuzzEvent): void {
	if (event.kind === READ_STATE_KIND) {
		const dOf = (candidate: BuzzEvent) => candidate.tags.find((tag) => tag[0] === "d")?.[1];
		const replaced = stored.findIndex(
			(candidate) => candidate.kind === event.kind && candidate.pubkey === event.pubkey && dOf(candidate) === dOf(event),
		);
		if (replaced >= 0) stored.splice(replaced, 1);
	}
	stored.push(event);
}

function answer(socket: ServerWebSocket<unknown>, raw: string): void {
	const [frameType, ...rest] = JSON.parse(raw) as [string, ...unknown[]];
	if (frameType === "AUTH" || frameType === "EVENT") {
		const event = rest[0] as BuzzEvent;
		if (frameType === "EVENT") keep(event);
		socket.send(JSON.stringify(["OK", event.id, true, ""]));
		return;
	}
	if (frameType !== "REQ") return;
	const [subscriptionID, ...filters] = rest as [string, ...Filter[]];
	for (const filter of filters) {
		const found = stored
			.filter((event) => matches(event, filter))
			.sort((first, second) => second.created_at - first.created_at)
			.slice(0, filter.limit ?? stored.length);
		for (const event of found) socket.send(JSON.stringify(["EVENT", subscriptionID, event]));
	}
	socket.send(JSON.stringify(["EOSE", subscriptionID]));
}

let server: Server<unknown>;
let relayURL = "";

beforeAll(() => {
	server = Bun.serve({
		port: 0,
		fetch(request, serving) {
			if (serving.upgrade(request, { data: undefined })) return undefined;
			return new Response("a relay speaks websocket", { status: 400 });
		},
		websocket: {
			open(socket) {
				socket.send(JSON.stringify(["AUTH", "challenge"]));
			},
			message(socket, raw) {
				answer(socket, String(raw));
			},
		},
	});
	relayURL = `ws://127.0.0.1:${server.port}`;
});

afterAll(() => {
	closeEveryPooledRelay();
	server.stop(true);
});

const reader = generateSecretKey();
const readerSecretHex = bytesToHex(reader);
const readerPubkey = getPublicKey(reader);
const writer = generateSecretKey();
const now = Math.floor(Date.now() / 1000);

function signed(secret: Uint8Array, kind: number, createdAt: number, tags: string[][], content = ""): BuzzEvent {
	return finalizeEvent({ kind, created_at: createdAt, tags, content }, secret) as BuzzEvent;
}

function postIn(channelID: string, secondsAgo: number, author = writer): BuzzEvent {
	const event = signed(author, 9, now - secondsAgo, [["h", channelID]], "hello");
	stored.push(event);
	return event;
}

function phoneReadThrough(channelID: string, readAt: number): void {
	const conversationKey = nip44.utils.getConversationKey(reader, readerPubkey);
	const blob = JSON.stringify({ v: 1, client_id: "buzz-phone", contexts: { [channelID]: readAt } });
	keep(
		signed(reader, READ_STATE_KIND, now, [["d", readStateDTag("f".repeat(32))], ["t", "read-state"]], nip44.encrypt(blob, conversationKey)),
	);
}

function unread(channelIDs: string[]): Promise<Map<string, number>> {
	return unreadCountsAsUser({ relayURL, userSecretHex: readerSecretHex, channelIDs });
}

describe("read state against a relay", () => {
	test("a conversation read through its latest message has nothing unread until someone writes again", async () => {
		postIn("channel-read", 300);
		postIn("channel-read", 200);
		expect((await unread(["channel-read"])).get("channel-read")).toBe(2);

		await markConversationReadAsUser({
			relayURL,
			userSecretHex: readerSecretHex,
			channelID: "channel-read",
			readAtSeconds: now - 200,
		});
		expect((await unread(["channel-read"])).get("channel-read")).toBe(0);

		postIn("channel-read", 100);
		postIn("channel-read", 50, reader);
		expect((await unread(["channel-read"])).get("channel-read")).toBe(1);
	});

	test("the read position is written encrypted to the reader and nobody else", async () => {
		const own = stored.find((event) => event.kind === READ_STATE_KIND && event.pubkey === readerPubkey);
		expect(own).toBeDefined();
		expect(own?.content).not.toContain("channel-read");
		const conversationKey = nip44.utils.getConversationKey(reader, readerPubkey);
		expect(JSON.parse(nip44.decrypt(own?.content ?? "", conversationKey)).contexts["channel-read"]).toBe(now - 200);
	});

	test("reading on another device counts here, and marking here leaves that device's record alone", async () => {
		postIn("channel-phone", 400);
		postIn("channel-phone", 300);
		postIn("channel-phone", 100);
		phoneReadThrough("channel-phone", now - 300);
		expect((await unread(["channel-phone"])).get("channel-phone")).toBe(1);

		await markConversationReadAsUser({
			relayURL,
			userSecretHex: readerSecretHex,
			channelID: "channel-phone",
			readAtSeconds: now - 100,
		});
		expect((await unread(["channel-phone"])).get("channel-phone")).toBe(0);
		const readStates = stored.filter((event) => event.kind === READ_STATE_KIND && event.pubkey === readerPubkey);
		expect(readStates).toHaveLength(2);
	});

	test("a message taken back is not unread", async () => {
		const taken = postIn("channel-taken", 100);
		postIn("channel-taken", 90);
		stored.push(signed(writer, 9005, now - 80, [["h", "channel-taken"], ["e", taken.id]]));
		expect((await unread(["channel-taken"])).get("channel-taken")).toBe(1);
	});
});
