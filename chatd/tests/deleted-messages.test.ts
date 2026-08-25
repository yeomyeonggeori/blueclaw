import { expect, test } from "bun:test";

import { BuzzAdapter } from "../src/adapters/buzz/adapter";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const KEPT = "a".repeat(64);
const TAKEN_BACK = "b".repeat(64);

function adapterAnswering(events: Record<number, unknown[]>): BuzzAdapter {
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
	(adapter as unknown as { relay: unknown }).relay = {
		query: async (filter: { kinds: number[] }) => events[filter.kinds[0]] ?? [],
	};
	(adapter as unknown as { fetchProfile: unknown }).fetchProfile = async () => null;
	return adapter;
}

function messageEvent(id: string, text: string) {
	return { id, pubkey: "c".repeat(64), created_at: 1784900000, kind: 9, tags: [["h", CHANNEL]], content: text, sig: "" };
}

// Somebody deletes a message to unsay it. The relay keeps what was said and
// records that it was taken back, and a reader that asks only for messages is
// handed words their author withdrew — which is how one wrong answer went on
// being read after it was deleted.
test("a message its author took back is not read back to the agent", async () => {
	const adapter = adapterAnswering({
		9: [messageEvent(KEPT, "목요일 팁스 연구노트 작성 일정 추가해줘"), messageEvent(TAKEN_BACK, "상하이 edatec 미팅을 찾지 못했습니다")],
		9005: [{ id: "d".repeat(64), pubkey: "c".repeat(64), created_at: 1784900001, kind: 9005, tags: [["h", CHANNEL], ["e", TAKEN_BACK]], content: "", sig: "" }],
	});

	const fetched = await adapter.fetchMessages(adapter.encodeThreadId({ channelId: CHANNEL }), { limit: 20 });

	expect(fetched.messages.map((message) => message.id)).toEqual([KEPT]);
});

test("nothing deleted leaves everything where it was", async () => {
	const adapter = adapterAnswering({
		9: [messageEvent(KEPT, "목요일 팁스 연구노트 작성 일정 추가해줘")],
		9005: [],
	});

	const fetched = await adapter.fetchMessages(adapter.encodeThreadId({ channelId: CHANNEL }), { limit: 20 });

	expect(fetched.messages.map((message) => message.id)).toEqual([KEPT]);
});
