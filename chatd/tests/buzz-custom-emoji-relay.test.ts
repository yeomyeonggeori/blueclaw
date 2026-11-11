import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey, getPublicKey } from "nostr-tools/pure";
import { bytesToHex } from "nostr-tools/utils";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import { unionOfEmojiSets } from "../src/adapters/buzz/user-emoji.ts";
import { EVENT_DELETION_KIND } from "../src/adapters/buzz/deletions.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";
import { personCapabilities } from "../src/personal/capabilities.ts";
import type { PersonalMessage } from "../src/personal/gateway.ts";
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

	test("a message names the time zone it was sent from, so the agent reads its times there", async () => {
		const { actor, gateway } = personWithEmoji();

		await gateway.sendMessage(actor, "channel-1", "재택 출근 아홉시", undefined, [], undefined, "America/Los_Angeles");

		const sent = kept.find((event) => event.kind === 9);
		expect(sent?.tags).toContainEqual(["tz", "America/Los_Angeles"]);
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

describe("a choice on Buzz, as a person reads and answers it", () => {
	test("an answer the person pressed names the question and the option it chose", async () => {
		const { actor, gateway } = personWithEmoji();

		await personCapabilities["person.message.send"]?.(gateway, {
			actor,
			conversationID: "channel-1",
			body: "승인",
			answer: { interactionID: "interaction-1", key: "approve" },
		});

		const sent = kept.find((event) => event.kind === 9);
		expect(sent?.content).toBe("승인");
		expect(sent?.tags).toContainEqual(["answer", "interaction-1", "approve"]);
	});

	test("a question reads back with its choices and its resolution, a failure as a failure", async () => {
		const agentKey = generateSecretKey();
		const now = Math.floor(Date.now() / 1000);
		const question = finalizeEvent(
			{
				kind: 9,
				content: "할까요?\n\n1. 승인\n2. 거절",
				tags: [["h", "channel-1"], ["ask", "interaction-1", "choice", ""], ["option", "approve", "승인"], ["option", "deny", "거절"]],
				created_at: now - 10,
			},
			agentKey,
		) as BuzzEvent;
		const resolution = finalizeEvent(
			{
				kind: 40003,
				content: question.content,
				tags: [["h", "channel-1"], ["e", question.id], ["resolved", "interaction-1", "deny"]],
				created_at: now - 5,
			},
			agentKey,
		) as BuzzEvent;
		const failure = finalizeEvent(
			{ kind: 9, content: "실패했어요", tags: [["h", "channel-1"], ["reply-kind", "error"]], created_at: now - 1 },
			agentKey,
		) as BuzzEvent;
		const forgedByAPerson = finalizeEvent(
			{
				kind: 9,
				content: "골라 보세요\n\n1. 예",
				tags: [["h", "channel-1"], ["ask", "interaction-9", "choice", ""], ["option", "yes", "예"]],
				created_at: now - 3,
			},
			generateSecretKey(),
		) as BuzzEvent;
		kept.push(question, resolution, failure, forgedByAPerson);
		const { actor, gateway } = personWithEmoji();

		const page = await personCapabilities["person.messages.list"]?.(
			gateway,
			{ actor, conversationID: "channel-1" },
			getPublicKey(agentKey),
		);
		const messages: PersonalMessage[] = page && "messages" in page && Array.isArray(page.messages) ? page.messages : [];

		const readQuestion = messages.find((message) => message.id === question.id);
		const readFailure = messages.find((message) => message.id === failure.id);
		const readForgery = messages.find((message) => message.id === forgedByAPerson.id);
		expect(readForgery?.body).toBe("골라 보세요\n\n1. 예");
		expect(readForgery?.ask).toBeUndefined();
		expect(readQuestion?.ask?.options.map((option) => option.key)).toEqual(["approve", "deny"]);
		expect(readQuestion?.ask?.resolution).toEqual({ key: "deny" });
		expect(readQuestion?.isFailure).toBeUndefined();
		expect(readFailure?.isFailure).toBe(true);
		expect(readFailure?.ask).toBeUndefined();
	});
});
