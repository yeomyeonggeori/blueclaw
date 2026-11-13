import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey, getPublicKey } from "nostr-tools/pure";
import { bytesToHex } from "nostr-tools/utils";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import { listInboxAsUser } from "../src/adapters/buzz/user-inbox.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

type Filter = {
	ids?: string[];
	kinds?: number[];
	authors?: string[];
	since?: number;
	until?: number;
	limit?: number;
} & Record<string, unknown>;

const stored: BuzzEvent[] = [];

function matches(event: BuzzEvent, filter: Filter): boolean {
	if (filter.ids && !filter.ids.includes(event.id)) return false;
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


function answer(socket: ServerWebSocket<unknown>, raw: string): void {
	const [frameType, ...rest] = JSON.parse(raw) as [string, ...unknown[]];
	if (frameType === "AUTH" || frameType === "EVENT") {
		const event = rest[0] as BuzzEvent;
		if (frameType === "EVENT") stored.push(event);
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

type UnreadThread = { channelID: string; rootID: string; latestReplyID: string };

const joinedChannelIDs = ["channel-1", "dm-1"];
let unreadThreads: UnreadThread[] = [];
const unreadMessageIDs = new Set<string>();

function sidebar(): Response {
	const channels = joinedChannelIDs.map((channelID) => ({
		channel_id: channelID,
		unread: { status: "exact", value: 0 },
		attention: { status: "exact", value: 0 },
		latest_message_id: null,
		threads: {
			complete: true,
			items: unreadThreads
				.filter((thread) => thread.channelID === channelID)
				.map((thread) => ({ root_id: thread.rootID, unread: { status: "exact", value: 1 }, latest_reply_id: thread.latestReplyID, latest_reply_at: now })),
		},
	}));
	return Response.json({ account: {}, channels, next_cursor: null });
}

function readStatuses(url: URL): Response {
	const targets: { message_ids: string[] }[] = JSON.parse(url.searchParams.get("targets") ?? "[]");
	const contexts = targets.map((target) => ({
		status: "available",
		messages: target.message_ids.map((messageID) => ({ message_id: messageID, status: unreadMessageIDs.has(messageID) ? "unread" : "read" })),
	}));
	return Response.json({ account: {}, contexts });
}

let server: Server<unknown>;
let relayURL = "";

beforeAll(() => {
	server = Bun.serve({
		port: 0,
		fetch(request, serving) {
			const url = new URL(request.url);
			if (url.pathname === "/buzz/v1/me/sidebar") return sidebar();
			if (url.pathname === "/buzz/v1/me/read-state") return readStatuses(url);
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

beforeEach(() => {
	stored.length = 0;
	unreadThreads = [];
	unreadMessageIDs.clear();
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

function inboxUntil(untilSeconds?: number) {
	return listInboxAsUser({ relayURL, userSecretHex: readerSecretHex, untilSeconds });
}

describe("a person's inbox against a relay", () => {
	test("holds the messages that call on the reader and the latest reply of each thread the relay counts unread, newest first", async () => {
		const ownRoot = post(reader, "channel-1", 600);
		post(writer, "channel-1", 500, replyTo(ownRoot));
		const latestReply = post(writer, "channel-1", 300, replyTo(ownRoot));
		const mention = post(writer, "dm-1", 400, [["p", readerPubkey]]);
		post(writer, "channel-elsewhere", 100, [["p", readerPubkey]]);
		post(reader, "channel-1", 50, [["p", readerPubkey]]);
		post(writer, "channel-1", 40);
		unreadThreads = [{ channelID: "channel-1", rootID: ownRoot.id, latestReplyID: latestReply.id }];

		const { items } = await inboxUntil();

		expect(items.map((item) => [item.message.id, item.reason])).toEqual([
			[latestReply.id, "thread_reply"],
			[mention.id, "mention"],
		]);
		expect(items[0]?.message.parentID).toBe(ownRoot.id);
	});

	test("a mention is unread exactly when the relay says so", async () => {
		const unread = post(writer, "channel-1", 200, [["p", readerPubkey]]);
		const read = post(writer, "channel-1", 100, [["p", readerPubkey]]);
		unreadMessageIDs.add(unread.id);

		const unreadOf = new Map((await inboxUntil()).items.map((item) => [item.message.id, item.isUnread]));

		expect(unreadOf).toEqual(new Map([[read.id, false], [unread.id, true]]));
	});

	test("a mention taken back leaves the inbox and an edited one shows its latest wording", async () => {
		const taken = post(writer, "channel-1", 20, [["p", readerPubkey]], "oops");
		const edited = post(writer, "channel-1", 10, [["p", readerPubkey]], "first wording");
		stored.push(
			finalizeEvent({ kind: 9005, created_at: now - 5, tags: [["h", "channel-1"], ["e", taken.id]], content: "" }, writer) as BuzzEvent,
			finalizeEvent({ kind: 40003, created_at: now - 5, tags: [["h", "channel-1"], ["e", edited.id]], content: "second wording" }, writer) as BuzzEvent,
		);

		const { items } = await inboxUntil();

		expect(items.map((item) => [item.message.id, item.message.body])).toEqual([[edited.id, "second wording"]]);
	});

	test("asked until a moment, answers the page of mentions before it and leaves unread threads to the first page", async () => {
		const root = post(reader, "channel-1", 20_000);
		const reply = post(writer, "channel-1", 5, replyTo(root));
		unreadThreads = [{ channelID: "channel-1", rootID: root.id, latestReplyID: reply.id }];
		const older = Array.from({ length: 55 }, (_, index) => post(writer, "channel-1", 10_000 + index, [["p", readerPubkey]]));

		const page = await inboxUntil(now - 10_000);

		expect(page.items).toHaveLength(50);
		expect(page.items[0]?.message.id).toBe(older[0]?.id);
		expect(page.hasMoreBefore).toBe(true);

		const last = await inboxUntil(now - 10_050);

		expect(last.items.map((item) => item.message.id)).toEqual(older.slice(50).map((event) => event.id));
		expect(last.hasMoreBefore).toBe(false);
	});
});
