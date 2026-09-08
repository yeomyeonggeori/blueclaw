import { describe, expect, test } from "bun:test";
import { createBridge, normalizedInboundRoutingOf } from "../src/bridge.ts";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { Message, type Message as MessageType, type Thread } from "chat";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { ReactionSummary } from "../src/visible-context.ts";

const channelID = "8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f";
const otherChannelID = "3b7e1a90-5c2d-4e8f-9a1b-6c5d4e3f2a1b";
const rootOneID = "a".repeat(64);
const rootTwoID = "b".repeat(64);
const senderID = "c".repeat(64);
const signature = "f".repeat(128);

function createAdapter(): BuzzAdapter {
	return new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
}

class EditObservingBuzzAdapter extends BuzzAdapter {
	editHandler?: Parameters<BuzzAdapter["onMessageEdit"]>[0];

	override async fetchMessages(): Promise<{ messages: [] }> {
		return { messages: [] };
	}

	override async fetchThread(): Promise<{ id: string; channelId: string; channelName: string; isDM: false; metadata: Record<string, never> }> {
		return { id: "thread", channelId: channelID, channelName: "test", isDM: false, metadata: {} };
	}

	override async getUser(): Promise<null> {
		return null;
	}

	override async fetchReactions(): Promise<Map<string, ReactionSummary[]>> {
		return new Map();
	}

	override onMessageEdit(handler: Parameters<BuzzAdapter["onMessageEdit"]>[0]): void {
		this.editHandler = handler;
		super.onMessageEdit(handler);
	}
}

function message(id: string, rootID?: string): Pick<MessageType<BuzzEvent>, "id" | "raw"> {
	const event: BuzzEvent = {
		id,
		pubkey: senderID,
		created_at: 1784900000,
		kind: 9,
		tags: rootID ? [["h", channelID], ["e", rootID, "", "root"]] : [["h", channelID]],
		content: "hello",
		sig: signature,
	};
	return { id, raw: event };
}

function thread(adapter: BuzzAdapter, channel: string, rootID?: string): Pick<Thread, "id"> {
	return { id: adapter.encodeThreadId({ channelId: channel, rootEventId: rootID }) };
}

describe("normalized inbound routing", () => {
	test("shares channel conversation scope across distinct root messages", () => {
		const adapter = createAdapter();
		const first = normalizedInboundRoutingOf(adapter, thread(adapter, channelID, rootOneID), message(rootOneID));
		const second = normalizedInboundRoutingOf(adapter, thread(adapter, channelID, rootTwoID), message(rootTwoID));

		expect(first.conversationID).toBe(`buzz:${channelID}`);
		expect(second.conversationID).toBe(first.conversationID);
		expect(first.isThread).toBe(false);
		expect(second.isThread).toBe(false);
	});

	test("keeps reply target and history scope while sharing channel conversation", () => {
		const adapter = createAdapter();
		const routing = normalizedInboundRoutingOf(
			adapter,
			thread(adapter, channelID, rootOneID),
			message("reply-1", rootOneID),
		);

		expect(routing.conversationID).toBe(`buzz:${channelID}`);
		expect(routing.historyScopeThreadID).toBe(`buzz:${channelID}:${rootOneID}`);
		expect(routing.replyTargetID).toBe(`buzz:${channelID}:${rootOneID}`);
		expect(routing.isThread).toBe(true);
	});

	test("does not collapse a different channel into the current channel", () => {
		const adapter = createAdapter();
		const routing = normalizedInboundRoutingOf(adapter, thread(adapter, otherChannelID), message("d".repeat(64)));

		expect(routing.conversationID).toBe(`buzz:${otherChannelID}`);
	});
});

describe("normalized edit forwarding", () => {
	test("keeps the original message identity and adds the edit event ID", async () => {
		const adapter = new EditObservingBuzzAdapter({
			relayURL: "ws://localhost:3000",
			privateKeyHex: "1".repeat(64),
			botDisplayName: "internkim",
		});
		const chat: Parameters<typeof createBridge>[0] = {
			onDirectMessage: () => undefined,
			onNewMention: () => undefined,
			onSubscribedMessage: () => undefined,
			onAction: () => undefined,
		};
		createBridge(
			chat,
			{
				botUserName: "internkim",
				blueclawBaseURL: "http://blueclaw.test",
				relayInboundURL: undefined,
				listenHostname: "127.0.0.1",
				blueclawIngressURL: undefined,
				admindBaseURL: undefined,
				listenPort: 18090,
				mattermost: undefined,
				buzz: undefined,
			},
			{ buzz: adapter },
		);
		const editHandler = adapter.editHandler;
		if (!editHandler) throw new Error("expected edit handler registration");

		const original = message(rootOneID).raw;
		const editedMessage = new Message({
			id: rootOneID,
			threadId: `buzz:${channelID}:${rootOneID}`,
			text: "edited text",
			formatted: { type: "root", children: [] },
			raw: original,
			author: { userId: senderID, userName: "sender", fullName: "Sender", isBot: false, isMe: false },
			metadata: { dateSent: new Date(), edited: true },
			attachments: [],
		});
		const originalFetch = globalThis.fetch;
		let payload: { messageID?: unknown; eventID?: unknown; prompt?: unknown } | undefined;
		globalThis.fetch = Object.assign(
			async (_input: RequestInfo | URL, init?: RequestInit) => {
				const document: unknown = JSON.parse(String(init?.body));
				if (typeof document !== "object" || document === null) throw new Error("expected object payload");
				const messageID = "messageID" in document ? document.messageID : undefined;
				const eventID = "eventID" in document ? document.eventID : undefined;
				const prompt = "prompt" in document ? document.prompt : undefined;
				payload = { messageID, eventID, prompt };
				return new Response("ok", { status: 200 });
			},
			{ preconnect: originalFetch.preconnect },
		);
		try {
			await editHandler({ eventID: "edit-event", threadID: editedMessage.threadId, message: editedMessage });
		} finally {
			globalThis.fetch = originalFetch;
		}

		expect(payload?.messageID).toBe(rootOneID);
		expect(payload?.eventID).toBe("edit-event");
		expect(payload?.prompt).toBe("edited text");
	});
});
