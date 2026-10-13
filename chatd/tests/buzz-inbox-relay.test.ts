import { afterAll, beforeAll, describe, expect, mock, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey, getPublicKey } from "nostr-tools/pure";
import { bytesToHex, hexToBytes } from "nostr-tools/utils";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import { createRelayConnection } from "../src/adapters/buzz/relay-connection.ts";
import { READ_STATE_KIND } from "../src/adapters/buzz/read-state.ts";
import { listInboxAsUser } from "../src/adapters/buzz/user-inbox.ts";
import { markConversationReadAsUser } from "../src/adapters/buzz/user-read-state.ts";
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

function post(author: Uint8Array, channelID: string, secondsAgo: number, extraTags: string[][] = [], content = "hello"): BuzzEvent {
	const event = finalizeEvent(
		{ kind: 9, created_at: now - secondsAgo, tags: [["h", channelID], ...extraTags], content },
		author,
	) as BuzzEvent;
	stored.push(event);
	return event;
}

function replyTo(root: BuzzEvent): string[][] {
	return [
		["e", root.id, "", "root"],
		["e", root.id, "", "reply"],
	];
}

function inbox() {
	return listInboxAsUser({
		relayURL,
		userSecretHex: readerSecretHex,
		directChannelIDs: ["dm-1"],
		groupChannelIDs: ["channel-1"],
	});
}

describe("a person's inbox against a relay", () => {
	test("holds what was written to them, what names them, and replies in threads they joined, newest first", async () => {
		const direct = post(writer, "dm-1", 500);
		const mention = post(writer, "channel-1", 400, [["p", readerPubkey]]);
		const ownRoot = post(reader, "channel-1", 300);
		const reply = post(writer, "channel-1", 200, replyTo(ownRoot));
		post(writer, "channel-1", 150);
		post(writer, "channel-elsewhere", 100, [["p", readerPubkey]]);
		post(reader, "dm-1", 50);

		const items = await inbox();

		expect(items.map((item) => [item.message.id, item.reason])).toEqual([
			[reply.id, "thread_reply"],
			[mention.id, "mention"],
			[direct.id, "direct"],
		]);
		expect(items.find((item) => item.reason === "thread_reply")?.message.parentID).toBe(ownRoot.id);
		expect(items.every((item) => item.isUnread)).toBe(true);
	});

	test("a reply read in its thread and a message read in its conversation are no longer unread", async () => {
		const reply = (await inbox()).find((item) => item.reason === "thread_reply");
		await markConversationReadAsUser({
			relayURL,
			userSecretHex: readerSecretHex,
			channelID: "channel-1",
			threadRootID: reply?.message.parentID,
			readAtSeconds: now - 200,
		});
		await markConversationReadAsUser({
			relayURL,
			userSecretHex: readerSecretHex,
			channelID: "dm-1",
			readAtSeconds: now - 500,
		});

		const unreadOf = new Map((await inbox()).map((item) => [item.reason, item.isUnread]));

		expect(unreadOf).toEqual(
			new Map([
				["thread_reply", false],
				["mention", true],
				["direct", false],
			]),
		);
	});

	test("a message taken back leaves the inbox", async () => {
		const taken = post(writer, "dm-1", 10, [], "oops");
		stored.push(finalizeEvent({ kind: 9005, created_at: now - 5, tags: [["h", "dm-1"], ["e", taken.id]], content: "" }, writer) as BuzzEvent);

		expect((await inbox()).map((item) => item.message.id)).not.toContain(taken.id);
	});
});
