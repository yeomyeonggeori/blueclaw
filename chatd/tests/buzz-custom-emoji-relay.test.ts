import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey } from "nostr-tools/pure";
import { bytesToHex } from "nostr-tools/utils";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import { unionOfEmojiSets } from "../src/adapters/buzz/user-emoji.ts";
import { EVENT_DELETION_KIND } from "../src/adapters/buzz/deletions.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

type Filter = { kinds?: number[]; "#d"?: string[]; "#e"?: string[]; ids?: string[] };

const kept: BuzzEvent[] = [];
const pictureBytes = new Uint8Array([137, 80, 78, 71]);

function wants(filter: Filter, event: BuzzEvent): boolean {
	if (filter.kinds && !filter.kinds.includes(event.kind)) return false;
	if (filter.ids && !filter.ids.includes(event.id)) return false;
	const tagged = (name: string) => event.tags.filter((tag) => tag[0] === name).map((tag) => tag[1]);
	if (filter["#d"] && !filter["#d"].some((value) => tagged("d").includes(value))) return false;
	return !filter["#e"] || filter["#e"].some((value) => tagged("e").includes(value));
}

function answer(socket: ServerWebSocket<unknown>, raw: string): void {
	const [frameType, ...rest] = JSON.parse(raw) as [string, ...unknown[]];
	if (frameType === "AUTH" || frameType === "EVENT") {
		const event = rest[0] as BuzzEvent;
		if (frameType === "EVENT") kept.push(event);
		socket.send(JSON.stringify(["OK", event.id, true, ""]));
		return;
	}
	if (frameType !== "REQ") return;
	const [subscriptionID, ...filters] = rest as [string, ...Filter[]];
	for (const event of kept.filter((candidate) => filters.some((filter) => wants(filter, candidate)))) {
		socket.send(JSON.stringify(["EVENT", subscriptionID, event]));
	}
	socket.send(JSON.stringify(["EOSE", subscriptionID]));
}

let server: Server<unknown>;
let relayURL = "";
let pictureURL = "";

beforeAll(() => {
	server = Bun.serve({
		port: 0,
		fetch(request, serving) {
			if (new URL(request.url).pathname === "/media/shake.png") {
				return new Response(pictureBytes, { headers: { "content-type": "image/png" } });
			}
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
	pictureURL = `http://127.0.0.1:${server.port}/media/shake.png`;
});

beforeEach(() => {
	kept.length = 0;
});

afterAll(() => {
	closeEveryPooledRelay();
	server.stop(true);
});

function emojiSet(emoji: string[][], createdAt = Math.floor(Date.now() / 1000)): BuzzEvent {
	return finalizeEvent(
		{ kind: 30030, content: "", tags: [["d", "buzz:custom-emoji"], ...emoji], created_at: createdAt },
		generateSecretKey(),
	) as BuzzEvent;
}

function personWithEmoji() {
	kept.push(emojiSet([["emoji", "shake", pictureURL]]));
	const actor = { kind: "buzz-secret", secret: bytesToHex(generateSecretKey()) };
	return { actor, gateway: createBuzzPersonalGateway({} as never, { relayURL }) };
}

describe("a community's custom emoji on Buzz", () => {
	test("is every member's set together, the latest set winning a shortcode they disagree on", () => {
		const older = emojiSet([["emoji", "Shake", "https://example.com/old.png"], ["emoji", "wave", "https://example.com/wave.png"]], 100);
		const newer = emojiSet([["emoji", ":shake:", "https://example.com/new.png"], ["emoji", "no url"]], 200);

		expect(unionOfEmojiSets([newer, older])).toEqual([
			{ shortcode: "shake", url: "https://example.com/new.png" },
			{ shortcode: "wave", url: "https://example.com/wave.png" },
		]);
	});

	test("is listed by name and drawn from the picture its set names", async () => {
		const { actor, gateway } = personWithEmoji();

		expect(await gateway.listCustomEmoji(actor)).toEqual([{ name: "shake" }]);
		const image = await gateway.readCustomEmojiImage(actor, "shake", 100_000);
		expect(image?.dataURL).toBe(`data:image/png;base64,${Buffer.from(pictureBytes).toString("base64")}`);
		expect(await gateway.readCustomEmojiImage(actor, "nobody-made-this", 100_000)).toBeNull();
	});

	test("a message using one names its picture, so every client can draw it", async () => {
		const { actor, gateway } = personWithEmoji();

		await gateway.sendMessage(actor, "channel-1", "좋아요 :shake: :unknown:");

		const sent = kept.find((event) => event.kind === 9);
		expect(sent?.tags.filter((tag) => tag[0] === "emoji")).toEqual([["emoji", "shake", pictureURL]]);
	});

	test("a reaction with one is its shortcode carrying the picture, and is taken back by name", async () => {
		const { actor, gateway } = personWithEmoji();
		const messageID = "a".repeat(64);

		await gateway.addReaction(actor, "channel-1", messageID, "shake");
		const reaction = kept.find((event) => event.kind === 7);
		expect(reaction?.content).toBe(":shake:");
		expect(reaction?.tags).toContainEqual(["emoji", "shake", pictureURL]);

		await gateway.removeReaction(actor, "channel-1", messageID, "shake");
		expect(kept.some((event) => event.kind === EVENT_DELETION_KIND && event.tags.some((tag) => tag[0] === "e" && tag[1] === reaction?.id))).toBe(true);
	});
});
