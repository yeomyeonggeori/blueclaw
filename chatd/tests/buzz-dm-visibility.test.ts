import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey, getPublicKey } from "nostr-tools/pure";
import { bytesToHex } from "nostr-tools/utils";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import {
	hideDirectMessageAsUser,
	revealDirectMessageAsUser,
} from "../src/adapters/buzz/user-dm-visibility.ts";
import { listUserConversations } from "../src/adapters/buzz/user-session.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const DM_OPEN_KIND = 41010;
const DM_HIDE_KIND = 41012;
const DM_VISIBILITY_KIND = 30622;
const GROUP_METADATA_KIND = 39000;
const GROUP_MEMBERS_KIND = 39002;

type Filter = { kinds?: number[]; limit?: number } & Record<string, unknown>;

const viewer = generateSecretKey();
const viewerSecretHex = bytesToHex(viewer);
const viewerPubkey = getPublicKey(viewer);
const counterpartPubkey = getPublicKey(generateSecretKey());
const relayKey = generateSecretKey();

let stored: BuzzEvent[] = [];
let published: BuzzEvent[] = [];
let openAnswersChannelID = "";

function matches(event: BuzzEvent, filter: Filter): boolean {
	if (filter.kinds && !filter.kinds.includes(event.kind)) return false;
	for (const [key, wanted] of Object.entries(filter)) {
		if (!key.startsWith("#") || !Array.isArray(wanted)) continue;
		if (!event.tags.some((tag) => tag[0] === key.slice(1) && wanted.includes(tag[1] ?? ""))) return false;
	}
	return true;
}

function relaySigned(kind: number, tags: string[][], createdAt: number): BuzzEvent {
	return finalizeEvent({ kind, created_at: createdAt, tags, content: "" }, relayKey) as BuzzEvent;
}

function answer(socket: ServerWebSocket<unknown>, raw: string): void {
	const [frameType, ...rest] = JSON.parse(raw) as [string, ...unknown[]];
	if (frameType === "AUTH") {
		socket.send(JSON.stringify(["OK", (rest[0] as BuzzEvent).id, true, ""]));
		return;
	}
	if (frameType === "EVENT") {
		const event = rest[0] as BuzzEvent;
		published.push(event);
		const message =
			event.kind === DM_OPEN_KIND ? `response:${JSON.stringify({ channel_id: openAnswersChannelID, created: false })}` : "";
		socket.send(JSON.stringify(["OK", event.id, true, message]));
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

beforeEach(() => {
	stored = [
		relaySigned(GROUP_METADATA_KIND, [["d", "dm-1"], ["t", "dm"]], 1),
		relaySigned(GROUP_MEMBERS_KIND, [["d", "dm-1"], ["p", viewerPubkey], ["p", counterpartPubkey]], 1),
	];
	published = [];
	openAnswersChannelID = "dm-1";
});

function hiddenSnapshot(channelIDs: string[], createdAt: number): BuzzEvent {
	return relaySigned(
		DM_VISIBILITY_KIND,
		[["d", viewerPubkey], ["p", viewerPubkey], ...channelIDs.map((channelID) => ["h", channelID])],
		createdAt,
	);
}

const request = () => ({ relayURL, userSecretHex: viewerSecretHex, channelID: "dm-1" });

describe("hiding a direct conversation", () => {
	test("asks the relay to hide that one conversation", async () => {
		await hideDirectMessageAsUser(request());
		const hide = published.find((event) => event.kind === DM_HIDE_KIND);
		expect(hide?.tags).toEqual([["h", "dm-1"]]);
	});

	test("the conversation list says which conversations the newest snapshot hides", async () => {
		stored.push(hiddenSnapshot(["dm-1"], 2));
		const [hidden] = await listUserConversations(relayURL, viewerSecretHex, { withProfiles: false });
		expect(hidden?.isHidden).toBe(true);

		stored.push(hiddenSnapshot([], 3));
		const [shown] = await listUserConversations(relayURL, viewerSecretHex, { withProfiles: false });
		expect(shown?.isHidden).toBe(false);
	});
});

describe("revealing a hidden direct conversation", () => {
	test("opens it again with the people in it", async () => {
		stored.push(hiddenSnapshot(["dm-1"], 2));
		await revealDirectMessageAsUser(request());
		const open = published.find((event) => event.kind === DM_OPEN_KIND);
		expect(open?.tags).toEqual([["p", counterpartPubkey]]);
	});

	test("leaves a conversation that is not hidden alone", async () => {
		await revealDirectMessageAsUser(request());
		expect(published.some((event) => event.kind === DM_OPEN_KIND)).toBe(false);
	});

	test("refuses when the relay opens a different conversation", async () => {
		stored.push(hiddenSnapshot(["dm-1"], 2));
		openAnswersChannelID = "dm-2";
		await expect(revealDirectMessageAsUser(request())).rejects.toThrow("answered dm-2");
	});
});
