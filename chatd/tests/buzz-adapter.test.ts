import { describe, expect, test } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { reactionContentOf } from "../src/mirror/reaction-emoji.ts";
import { firstTagValue, threadTagsOf, type BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL_UUID = "8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f";
const ROOT_EVENT_ID = "a".repeat(64);
const SENDER_HEX = "c".repeat(64);
const AGENT_SECRET = "1".repeat(64);

function createAdapter(): BuzzAdapter {
	return new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: AGENT_SECRET,
		botDisplayName: "internkim",
	});
}

function createEvent(overrides: Partial<BuzzEvent> = {}): BuzzEvent {
	return {
		id: "e".repeat(64),
		pubkey: SENDER_HEX,
		created_at: 1784900000,
		kind: 9,
		tags: [["h", CHANNEL_UUID]],
		content: "@internkim hello",
		sig: "f".repeat(128),
		...overrides,
	};
}

describe("buzz thread id codec", () => {
	test("round-trips channel and root", () => {
		const adapter = createAdapter();
		const threadId = adapter.encodeThreadId({ channelId: CHANNEL_UUID, rootEventId: ROOT_EVENT_ID });
		expect(adapter.decodeThreadId(threadId)).toEqual({ channelId: CHANNEL_UUID, rootEventId: ROOT_EVENT_ID });
		expect(adapter.channelIdFromThreadId(threadId)).toBe(CHANNEL_UUID);
	});

	test("channel-only thread id omits root", () => {
		const adapter = createAdapter();
		const threadId = adapter.encodeThreadId({ channelId: CHANNEL_UUID });
		expect(adapter.decodeThreadId(threadId)).toEqual({ channelId: CHANNEL_UUID, rootEventId: undefined });
	});

	test("rejects foreign thread ids", () => {
		const adapter = createAdapter();
		expect(() => adapter.decodeThreadId("mattermost:abc")).toThrow();
	});
});

describe("buzz event mapping", () => {
	test("thread tags prefer marked root", () => {
		const tags = threadTagsOf(
			createEvent({
				tags: [
					["h", CHANNEL_UUID],
					["e", ROOT_EVENT_ID, "", "root"],
					["e", "b".repeat(64), "", "reply"],
				],
			}),
		);
		expect(tags.rootEventId).toBe(ROOT_EVENT_ID);
		expect(tags.parentEventId).toBe("b".repeat(64));
	});

	test("parseMessage maps a channel event into a Message", () => {
		const adapter = createAdapter();
		const message = adapter.parseMessage(createEvent());
		expect(message.text).toBe("@internkim hello");
		expect(message.author.userId).toBe(SENDER_HEX);
		expect(message.threadId).toBe(`buzz:${CHANNEL_UUID}:${"e".repeat(64)}`);
		expect(message.metadata.dateSent.toISOString()).toBe(new Date(1784900000 * 1000).toISOString());
	});

	test("reply events thread under their root", () => {
		const adapter = createAdapter();
		const message = adapter.parseMessage(
			createEvent({ tags: [["h", CHANNEL_UUID], ["e", ROOT_EVENT_ID, "", "root"]] }),
		);
		expect(message.threadId).toBe(`buzz:${CHANNEL_UUID}:${ROOT_EVENT_ID}`);
	});

	test("firstTagValue reads the channel tag", () => {
		expect(firstTagValue(createEvent(), "h")).toBe(CHANNEL_UUID);
	});

	test("reply-only tags resolve the parent as thread root", () => {
		const tags = threadTagsOf(
			createEvent({ tags: [["h", CHANNEL_UUID], ["e", ROOT_EVENT_ID, "", "reply"]] }),
		);
		expect(tags.rootEventId).toBe(ROOT_EVENT_ID);
		expect(tags.parentEventId).toBe(ROOT_EVENT_ID);
	});
});

describe("buzz history scope", () => {
	test("fresh root messages use channel scope", () => {
		const adapter = createAdapter();
		const threadId = adapter.encodeThreadId({ channelId: CHANNEL_UUID, rootEventId: "e".repeat(64) });
		expect(adapter.historyScopeThreadId(threadId, "e".repeat(64))).toBe(`buzz:${CHANNEL_UUID}`);
	});

	test("thread replies keep thread scope", () => {
		const adapter = createAdapter();
		const threadId = adapter.encodeThreadId({ channelId: CHANNEL_UUID, rootEventId: ROOT_EVENT_ID });
		expect(adapter.historyScopeThreadId(threadId, "e".repeat(64))).toBe(threadId);
	});
});

describe("buzz reactions", () => {
	test("maps every name in the blueclaw reaction vocabulary to unicode", async () => {
		const vocabularySource = await Bun.file(
			new URL("../../.dependency/bluecollar/agentcontract/reaction_emoji.go", import.meta.url),
		).text();
		const names = [...vocabularySource.matchAll(/"([^"]+)"/g)].map((match) => match[1] ?? "");
		expect(names.length).toBeGreaterThan(10);
		for (const name of names) {
			const content = reactionContentOf(name);
			expect(content).not.toBe(name);
			expect(content).toMatch(/[^\x20-\x7E]/);
		}
	});

	test("maps representative names to the expected characters", () => {
		expect(reactionContentOf("eyes")).toBe("👀");
		expect(reactionContentOf("rocket")).toBe("🚀");
		expect(reactionContentOf("clap")).toBe("👏");
		expect(reactionContentOf("+1")).toBe("👍");
	});

	test("passes unicode reactions through unchanged", () => {
		expect(reactionContentOf("👀")).toBe("👀");
	});
});

describe("buzz addressing", () => {
	test("reads bot and other mentions from p tags", () => {
		const adapter = createAdapter();
		const addressing = adapter.addressingOf(
			createEvent({ tags: [["h", CHANNEL_UUID], ["p", adapter.botPubkey], ["p", "d".repeat(64)]] }),
		);
		expect(addressing).toEqual({ botMentioned: true, otherPersonMentioned: true });
	});

	test("handles events without p tags", () => {
		const adapter = createAdapter();
		expect(adapter.addressingOf(createEvent())).toEqual({ botMentioned: false, otherPersonMentioned: false });
	});
});
