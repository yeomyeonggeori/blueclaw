import { expect, test } from "bun:test";

import { BuzzAdapter } from "../src/adapters/buzz/adapter";

const ASKED_FROM = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const LIVES_IN = "71031c16-5569-8595-06ea-1e3caf3dd83f";
const TARGET = "a".repeat(64);

function adapterOver(events: Record<string, unknown>) {
	const published: { kind: number; tags: string[][] }[] = [];
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
	(adapter as unknown as { relay: unknown }).relay = {
		query: async (filter: { ids?: string[] }) =>
			filter.ids ? filter.ids.map((id) => events[id]).filter(Boolean) : [],
		publish: async (kind: number, _content: string, tags: string[][]) => {
			published.push({ kind, tags });
			return { id: "e".repeat(64), kind, tags, pubkey: "", created_at: 0, content: "", sig: "" };
		},
	};
	return { adapter, published };
}

function channelTagOf(published: { tags: string[][] }): string | undefined {
	return published.tags.find((tag) => tag[0] === "h")?.[1];
}

// "그 잡담 글 지워줘" asked in a DM must land its deletion in 잡담: the relay
// rejects a control event whose channel tag names somewhere else.
test("a deletion lands in the channel its target lives in", async () => {
	const { adapter, published } = adapterOver({
		[TARGET]: { id: TARGET, kind: 9, tags: [["h", LIVES_IN]], content: "중복 글", pubkey: "b".repeat(64), created_at: 1, sig: "" },
	});

	await adapter.deleteMessage(adapter.encodeThreadId({ channelId: ASKED_FROM }), TARGET);

	expect(channelTagOf(published[0]!)).toBe(LIVES_IN);
});

test("an edit lands in the channel its target lives in", async () => {
	const { adapter, published } = adapterOver({
		[TARGET]: { id: TARGET, kind: 9, tags: [["h", LIVES_IN]], content: "원문", pubkey: "b".repeat(64), created_at: 1, sig: "" },
	});

	await adapter.editMessage(adapter.encodeThreadId({ channelId: ASKED_FROM }), TARGET, "고친 글");

	expect(channelTagOf(published[0]!)).toBe(LIVES_IN);
});

test("a target the relay no longer holds falls back to the asking conversation", async () => {
	const { adapter, published } = adapterOver({});

	await adapter.deleteMessage(adapter.encodeThreadId({ channelId: ASKED_FROM }), TARGET);

	expect(channelTagOf(published[0]!)).toBe(ASKED_FROM);
});
