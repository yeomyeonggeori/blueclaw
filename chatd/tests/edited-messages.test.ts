import { expect, test } from "bun:test";

import { BuzzAdapter } from "../src/adapters/buzz/adapter";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const NARRATED = "a".repeat(64);
const UNTOUCHED = "b".repeat(64);
const AUTHOR = "c".repeat(64);

function adapterAnswering(events: Record<number, unknown[]>): BuzzAdapter {
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
	(adapter as unknown as { relay: unknown }).relay = {
		query: async (filter: { kinds?: number[] }) => events[filter.kinds?.[0] ?? 0] ?? [],
	};
	(adapter as unknown as { fetchProfile: unknown }).fetchProfile = async () => null;
	return adapter;
}

function messageEvent(id: string, text: string, createdAt = 1784900000) {
	return { id, pubkey: AUTHOR, created_at: createdAt, kind: 9, tags: [["h", CHANNEL]], content: text, sig: "" };
}

function editEvent(id: string, targetId: string, text: string, createdAt: number) {
	return { id, pubkey: AUTHOR, created_at: createdAt, kind: 40003, tags: [["h", CHANNEL], ["e", targetId]], content: text, sig: "" };
}

// The agent narrates its work by editing one progress message until it holds
// the answer. A reader that ignores edits shows the first draft forever — which
// is how a finished failure notice went on reading as "message_context".
test("a channel reader is handed the words the author last meant", async () => {
	const adapter = adapterAnswering({
		9: [messageEvent(NARRATED, "message_context"), messageEvent(UNTOUCHED, "그대로인 메시지")],
		40003: [
			editEvent("d".repeat(64), NARRATED, "message_context ✗", 1784900001),
			editEvent("e".repeat(64), NARRATED, "삭제하지 못했습니다. 잡담 채널에서 다시 요청해 주세요.", 1784900002),
		],
	});

	const fetched = await adapter.fetchMessages(adapter.encodeThreadId({ channelId: CHANNEL }), { limit: 20 });

	const byId = new Map(fetched.messages.map((message) => [message.id, message]));
	expect(byId.get(NARRATED)?.text).toBe("삭제하지 못했습니다. 잡담 채널에서 다시 요청해 주세요.");
	expect(byId.get(NARRATED)?.metadata?.edited).toBe(true);
	expect(byId.get(UNTOUCHED)?.text).toBe("그대로인 메시지");
	expect(byId.get(UNTOUCHED)?.metadata?.edited).toBe(false);
});

test("a thread reader applies edits the same way", async () => {
	const root = messageEvent(UNTOUCHED, "스레드 루트");
	const replyEvent = {
		id: NARRATED,
		pubkey: AUTHOR,
		created_at: 1784900001,
		kind: 9,
		tags: [["h", CHANNEL], ["e", UNTOUCHED, "", "root"]],
		content: "message_context",
		sig: "",
	};
	const adapter = adapterAnswering({
		9: [root, replyEvent],
		40003: [editEvent("d".repeat(64), NARRATED, "고쳐 쓴 답장", 1784900002)],
	});

	const fetched = await adapter.fetchMessages(
		adapter.encodeThreadId({ channelId: CHANNEL, rootEventId: UNTOUCHED }),
		{ limit: 20 },
	);

	const reply = fetched.messages.find((message) => message.id === NARRATED);
	expect(reply?.text).toBe("고쳐 쓴 답장");
});
