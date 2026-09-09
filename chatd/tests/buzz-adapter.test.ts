import { describe, expect, test } from "bun:test";
import { createMemoryState } from "@chat-adapter/state-memory";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { reactionContentOf } from "../src/mirror/reaction-emoji.ts";
import { Chat, type Message } from "chat";
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

	// A direct conversation threads the same way a channel does. Reading a reply
	// against the whole conversation is how one request came to be answered with
	// the subject of an older one that happened to share the channel.
	test("a reply in a direct conversation keeps thread scope", () => {
		const adapter = createAdapter();
		(adapter as unknown as { channelsById: Map<string, unknown> }).channelsById.set(CHANNEL_UUID, {
			channelId: CHANNEL_UUID,
			name: 'direct',
			isDM: true
		});
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
const OTHER_CHANNEL_UUID = "3b7e1a90-5c2d-4e8f-9a1b-6c5d4e3f2a1b";

type RelayInjectable = {
	relay: {
		pubkeyHex?: string;
		query: (filter: object) => Promise<BuzzEvent[]>;
		publish: (kind: number, content: string, tags: string[][]) => Promise<BuzzEvent>;
		subscribe: (filters: object[], onEvent: (event: BuzzEvent) => void) => void;
	};
};

type IncomingEventInjectable = RelayInjectable & {
	channelsById: Map<string, unknown>;
	chat: {
		processMessage: (
			adapter: BuzzAdapter,
			threadId: string,
			messageFactory: () => Promise<Message<BuzzEvent>>,
		) => Promise<void>;
	} | null;
	dispatchIncomingEvent: (event: BuzzEvent) => Promise<void>;
};

function adapterWithRelay(knownEvents: BuzzEvent[]) {
	const adapter = createAdapter();
	const published: Array<{ kind: number; tags: string[][] }> = [];
	(adapter as unknown as RelayInjectable).relay = {
		query: async () => knownEvents,
		publish: async (kind, _content, tags) => {
			published.push({ kind, tags });
			return createEvent();
		},
		subscribe: () => undefined,
	};
	return { adapter, published };
}

describe("buzz cross-channel edit and delete", () => {
	test("delete stamps the channel the target lives in", async () => {
		const target = createEvent({ tags: [["h", OTHER_CHANNEL_UUID]] });
		const { adapter, published } = adapterWithRelay([target]);
		await adapter.deleteMessage(adapter.encodeThreadId({ channelId: CHANNEL_UUID }), target.id);
		expect(published[0]?.tags).toContainEqual(["h", OTHER_CHANNEL_UUID]);
	});

	test("delete falls back to the conversation channel for an unknown target", async () => {
		const { adapter, published } = adapterWithRelay([]);
		await adapter.deleteMessage(adapter.encodeThreadId({ channelId: CHANNEL_UUID }), "d".repeat(64));
		expect(published[0]?.tags).toContainEqual(["h", CHANNEL_UUID]);
	});

	test("edit stamps the channel the target lives in", async () => {
		const target = createEvent({ tags: [["h", OTHER_CHANNEL_UUID]] });
		const { adapter, published } = adapterWithRelay([target]);
		await adapter.editMessage(adapter.encodeThreadId({ channelId: CHANNEL_UUID }), target.id, "edited");
		expect(published[0]?.tags).toContainEqual(["h", OTHER_CHANNEL_UUID]);
	});
});

describe("buzz inbound edits", () => {
	test("forwards an authored root edit with its original thread and unique event ID", async () => {
		const original = createEvent({ id: ROOT_EVENT_ID, content: "original" });
		const edit = createEvent({
			id: "d".repeat(64),
			kind: 40003,
			content: "edited",
			tags: [["h", CHANNEL_UUID], ["e", ROOT_EVENT_ID]],
			created_at: 1784900001,
		});
		const adapter = createAdapter();
		const received: Array<{ eventID: string; threadID: string; message: Message }> = [];
		(adapter as unknown as IncomingEventInjectable).relay = {
			query: async () => [original],
			publish: async (_kind, _content, _tags) => original,
			subscribe: () => undefined,
		};
		(adapter as unknown as IncomingEventInjectable).channelsById.set(CHANNEL_UUID, {});
		(adapter as unknown as IncomingEventInjectable).chat = { processMessage: async () => undefined };
		adapter.onMessageEdit(async (receivedEdit) => {
			received.push(receivedEdit);
		});

		await (adapter as unknown as IncomingEventInjectable).dispatchIncomingEvent(edit);

		expect(received).toHaveLength(1);
		expect(received[0]?.eventID).toBe(edit.id);
		expect(received[0]?.message.id).toBe(original.id);
		expect(received[0]?.message.raw).toBe(original);
		expect(received[0]?.message.text).toBe("edited");
		expect(received[0]?.message.metadata.edited).toBe(true);
		expect(received[0]?.threadID).toBe(`buzz:${CHANNEL_UUID}:${ROOT_EVENT_ID}`);
	});

	test("rejects edits from another author or channel", async () => {
		const original = createEvent({ id: ROOT_EVENT_ID });
		const adapter = createAdapter();
		const received: string[] = [];
		(adapter as unknown as IncomingEventInjectable).relay = {
			query: async () => [original],
			publish: async (_kind, _content, _tags) => original,
			subscribe: () => undefined,
		};
		(adapter as unknown as IncomingEventInjectable).channelsById.set(CHANNEL_UUID, {});
		(adapter as unknown as IncomingEventInjectable).chat = { processMessage: async () => undefined };
		adapter.onMessageEdit(async (edit) => {
			received.push(edit.eventID);
		});

		await (adapter as unknown as IncomingEventInjectable).dispatchIncomingEvent(
			createEvent({ kind: 40003, tags: [["h", CHANNEL_UUID], ["e", ROOT_EVENT_ID]], pubkey: "b".repeat(64) }),
		);
		await (adapter as unknown as IncomingEventInjectable).dispatchIncomingEvent(
			createEvent({ kind: 40003, tags: [["h", OTHER_CHANNEL_UUID], ["e", ROOT_EVENT_ID]] }),
		);

		expect(received).toEqual([]);
	});

	test("keeps a thread edit on the original root thread", async () => {
		const originalID = "9".repeat(64);
		const original = createEvent({ id: originalID, tags: [["h", CHANNEL_UUID], ["e", ROOT_EVENT_ID, "", "root"]] });
		const edit = createEvent({
			id: "8".repeat(64),
			kind: 40003,
			tags: [["h", CHANNEL_UUID], ["e", originalID]],
		});
		const adapter = createAdapter();
		const received: string[] = [];
		(adapter as unknown as IncomingEventInjectable).relay = {
			query: async () => [original],
			publish: async (_kind, _content, _tags) => original,
			subscribe: () => undefined,
		};
		(adapter as unknown as IncomingEventInjectable).channelsById.set(CHANNEL_UUID, {});
		(adapter as unknown as IncomingEventInjectable).chat = { processMessage: async () => undefined };
		adapter.onMessageEdit(async (editEvent) => {
			received.push(editEvent.threadID);
		});

		await (adapter as unknown as IncomingEventInjectable).dispatchIncomingEvent(edit);

		expect(received).toEqual([`buzz:${CHANNEL_UUID}:${ROOT_EVENT_ID}`]);
	});
});

describe("buzz inbound addressing", () => {
	test("marks a stream root addressed by a bot p tag without requiring an @ mention", async () => {
		const adapter = createAdapter();
		const botPubkey = adapter.botPubkey;
		(adapter as unknown as IncomingEventInjectable).relay = {
			pubkeyHex: botPubkey,
			query: async () => [],
			publish: async (_kind, _content, _tags) => createEvent(),
			subscribe: () => undefined,
		};
		(adapter as unknown as IncomingEventInjectable).channelsById.set(CHANNEL_UUID, {});
		const state = createMemoryState();
		await state.connect();
		const chat = new Chat({
			userName: "internkim",
			state,
			concurrency: "queue",
			adapters: { buzz: adapter },
		});
		let mentionCount = 0;
		let subscribedMessageCount = 0;
		chat.onNewMention(async () => {
			mentionCount += 1;
		});
		chat.onSubscribedMessage(async () => {
			subscribedMessageCount += 1;
		});
		(adapter as unknown as { chat: Chat }).chat = chat;

		await (adapter as unknown as IncomingEventInjectable).dispatchIncomingEvent(
			createEvent({
				content: "please handle this",
				tags: [["h", CHANNEL_UUID], ["p", botPubkey]],
			}),
		);

		await (adapter as unknown as IncomingEventInjectable).dispatchIncomingEvent(
			createEvent({
				id: "d".repeat(64),
				content: "other person only",
				tags: [["h", CHANNEL_UUID], ["p", "d".repeat(64)]],
			}),
		);

		expect(mentionCount).toBe(1);
		expect(subscribedMessageCount).toBe(0);
	});
});
