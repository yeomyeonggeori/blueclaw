import { describe, expect, test } from "bun:test";
import {
	buildVisibleContext,
	decodeHistoryCursor,
	encodeHistoryCursor,
	type ContextCapableAdapter,
} from "../src/visible-context.ts";

function contextMessage(id: string, text: string, sentAtSecond: number, threadRootId = id) {
	return {
		id,
		text,
		author: { userId: `user-${id}`, userName: `handle-${id}`, fullName: `Name ${id}` },
		metadata: { dateSent: new Date(sentAtSecond * 1000) },
		raw: { threadRootId },
	};
}

function fakeAdapter(messages: ReturnType<typeof contextMessage>[], isDM = false): ContextCapableAdapter {
	return {
		name: "fake",
		async fetchMessages() {
			return { messages };
		},
		async fetchThread(threadId: string) {
			return { id: threadId, channelId: "channel-1", channelName: "general", isDM, metadata: {} };
		},
		async getUser(userId: string) {
			return { userId, userName: "sender-handle", fullName: "Sender Name", email: "sender@test", isBot: false };
		},
		threadRootIdOf(raw: unknown) {
			if (typeof raw !== "object" || raw === null || !("threadRootId" in raw)) return undefined;
			return typeof raw.threadRootId === "string" ? raw.threadRootId : undefined;
		},
	};
}

describe("history cursor codec", () => {
	test("round-trips thread and cursor", () => {
		const encoded = encodeHistoryCursor({ threadId: "fake:channel-1", cursor: "page-2" });
		expect(decodeHistoryCursor(encoded)).toEqual({ threadId: "fake:channel-1", cursor: "page-2" });
	});

	test("treats opaque values as bare thread ids", () => {
		expect(decodeHistoryCursor("fake:channel-1")).toEqual({ threadId: "fake:channel-1" });
	});
});

describe("buildVisibleContext", () => {
	test("returns messages before the triggering message with sender and channel info", async () => {
		const adapter = fakeAdapter([
			contextMessage("a", "first", 100),
			contextMessage("b", "second", 200),
			contextMessage("c", "trigger", 300),
		]);
		const context = await buildVisibleContext(adapter, "fake:channel-1", {
			beforeMessageId: "c",
			senderId: "user-c",
		});
		expect(context.messages.map((message) => message.text)).toEqual(["first", "second"]);
		expect(context.messages[0]).toMatchObject({
			id: "a",
			speaker: "Name a",
			speakerHandle: "handle-a",
			senderId: "user-a",
			text: "first",
			sentAt: new Date(100 * 1000).toISOString(),
			isBot: false,
		});
		expect(context.sender).toEqual({
			platform: "fake",
			senderID: "user-c",
			handle: "sender-handle",
			email: "sender@test",
			name: "Sender Name",
		});
		expect(context.channelID).toBe("channel-1");
		expect(context.channelName).toBe("general");
		expect(context.conversationType).toBe("channel");
		expect(context.hasMoreBefore).toBe(false);
	});

	test("trims to the limit and reports more history", async () => {
		const messages = Array.from({ length: 5 }, (_, index) =>
			contextMessage(`m${index}`, `message ${index}`, 100 + index),
		);
		const adapter = fakeAdapter([...messages, contextMessage("t", "trigger", 900)]);
		const context = await buildVisibleContext(adapter, "fake:channel-1", { beforeMessageId: "t", limit: 3 });
		expect(context.messages.map((message) => message.text)).toEqual(["message 2", "message 3", "message 4"]);
		expect(context.hasMoreBefore).toBe(true);
	});

	test("drops the triggering message when it is not in the window", async () => {
		const adapter = fakeAdapter([contextMessage("a", "first", 100)]);
		const context = await buildVisibleContext(adapter, "fake:channel-1", { beforeMessageId: "missing" });
		expect(context.messages.map((message) => message.text)).toEqual(["first"]);
	});

	test("keeps recent replies in direct conversation history", async () => {
		const adapter = fakeAdapter(
			[
				contextMessage("root-a", "old root", 100),
				contextMessage("reply-a", "old reply", 200, "root-a"),
				contextMessage("root-b", "recent root", 300),
				contextMessage("reply-b", "recent reply", 400, "root-b"),
				contextMessage("trigger", "new question", 500, "root-b"),
			],
			true,
		);
		const context = await buildVisibleContext(adapter, "fake:dm-1", {
			beforeMessageId: "trigger",
			onlyExchangeOpenings: true,
		});

		expect(context.messages.map((message) => message.id)).toEqual(["root-a", "reply-a", "root-b", "reply-b"]);
		expect(context.messagesOpenOtherExchanges).toBe(false);
		expect(context.conversationType).toBe("direct");
	});

	test("keeps channel history limited to exchange openings", async () => {
		const adapter = fakeAdapter([
			contextMessage("root-a", "old root", 100),
			contextMessage("reply-a", "old reply", 200, "root-a"),
			contextMessage("root-b", "recent root", 300),
			contextMessage("reply-b", "recent reply", 400, "root-b"),
			contextMessage("trigger", "new question", 500, "root-b"),
		]);
		const context = await buildVisibleContext(adapter, "fake:channel-1", {
			beforeMessageId: "trigger",
			onlyExchangeOpenings: true,
		});

		expect(context.messages.map((message) => message.id)).toEqual(["root-a", "root-b"]);
		expect(context.messagesOpenOtherExchanges).toBe(true);
	});

	test("keeps a scoped direct thread within the fetched thread history", async () => {
		const adapter = fakeAdapter(
			[
				contextMessage("root-b", "recent root", 300),
				contextMessage("reply-b", "recent reply", 400, "root-b"),
				contextMessage("trigger", "new question", 500, "root-b"),
			],
			true,
		);
		const context = await buildVisibleContext(adapter, "fake:dm-thread-b", {
			beforeMessageId: "trigger",
			onlyExchangeOpenings: true,
		});

		expect(context.messages.map((message) => message.id)).toEqual(["root-b", "reply-b"]);
		expect(context.messagesOpenOtherExchanges).toBe(false);
	});
});
