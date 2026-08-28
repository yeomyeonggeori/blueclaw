import { expect, test } from "bun:test";

import { searchScore, rankBySearchScore } from "../src/message-search";
import { BuzzAdapter } from "../src/adapters/buzz/adapter";
import { createOutboundHandler } from "../src/outbound";
import type { ChatdConfiguration } from "../src/configuration";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_SECRET = "1".repeat(64);
const REQUESTER = "f".repeat(64);

test("a verbatim substring outranks a token overlap", () => {
	expect(searchScore("내일 회식 장소는 강남입니다", ["회식 장소"])).toBe(1);
	expect(searchScore("내일 회식은 강남에서요", ["회식 장소"])).toBeGreaterThan(0);
	expect(searchScore("내일 회식은 강남에서요", ["회식 장소"])).toBeLessThan(1);
	expect(searchScore("오늘 날씨가 좋네요", ["회식 장소"])).toBe(0);
});

test("a partial token still finds the message it sits in", () => {
	expect(searchScore("회식은 다음 주로 미룹니다", ["회식"])).toBeGreaterThan(0);
	expect(searchScore("the meetings moved to friday", ["meeting"])).toBeGreaterThan(0);
});

test("no queries means everything matches in recency order", () => {
	const ranked = rankBySearchScore(
		[
			{ id: "old", text: "먼저 쓴 글", at: 1 },
			{ id: "new", text: "나중에 쓴 글", at: 2 },
		],
		[],
		(entry) => entry.text,
		(entry) => entry.at,
		10,
	);
	expect(ranked.map((entry) => entry.candidate.id)).toEqual(["new", "old"]);
});

function adapterAnswering(events: Record<number, unknown[]>): BuzzAdapter {
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: BOT_SECRET,
		botDisplayName: "internkim",
	});
	(adapter as unknown as { relay: { query: unknown; pubkeyHex: string } }).relay = {
		pubkeyHex: adapter.botPubkey ?? "",
		query: async (filter: { kinds?: number[] }) => events[filter.kinds?.[0] ?? 0] ?? [],
	};
	return adapter;
}

function messageEvent(id: string, pubkey: string, text: string, createdAt: number) {
	return { id, pubkey, created_at: createdAt, kind: 9, tags: [["h", CHANNEL]], content: text, sig: "" };
}

test("message.search finds the assistant's own post by fuzzy keywords", async () => {
	const adapter = adapterAnswering({ 9: [], 40003: [], 9005: [] });
	const botPubkey = adapter.botPubkey;
	const events = {
		9: [
			messageEvent("a".repeat(64), botPubkey, "오늘 점심 공지: 상하이 미팅 결과를 공유합니다", 100),
			messageEvent("b".repeat(64), REQUESTER, "상하이 미팅은 어땠나요?", 101),
			messageEvent("c".repeat(64), botPubkey, "날씨가 좋네요", 102),
		],
		40003: [],
		9005: [],
	};
	(adapter as unknown as { relay: { query: unknown; pubkeyHex: string } }).relay = {
		pubkeyHex: botPubkey,
		query: async (filter: { kinds?: number[] }) => events[(filter.kinds?.[0] ?? 0) as 9] ?? [],
	};

	const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);
	const response = await handler(
		new Request("http://chatd/v1/platform/buzz/message.search", {
			method: "POST",
			body: JSON.stringify({
				channelID: CHANNEL,
				authoredBy: "assistant",
				queries: ["상하이 미팅"],
			}),
		}),
	);

	expect(response.status).toBe(200);
	const body = (await response.json()) as {
		channelID: string;
		candidates: { messageID: string; authoredByAssistant: boolean; score: number }[];
	};
	expect(body.channelID).toBe(CHANNEL);
	expect(body.candidates.map((candidate) => candidate.messageID)).toEqual(["a".repeat(64)]);
	expect(body.candidates[0]?.authoredByAssistant).toBe(true);
});

test("message.search applies edits before matching", async () => {
	const adapter = adapterAnswering({ 9: [], 40003: [], 9005: [] });
	const botPubkey = adapter.botPubkey;
	const events: Record<number, unknown[]> = {
		9: [messageEvent("a".repeat(64), botPubkey, "message_context", 100)],
		40003: [
			{
				id: "d".repeat(64),
				pubkey: botPubkey,
				created_at: 101,
				kind: 40003,
				tags: [["h", CHANNEL], ["e", "a".repeat(64)]],
				content: "삭제하지 못했습니다",
				sig: "",
			},
		],
		9005: [],
	};
	(adapter as unknown as { relay: { query: unknown; pubkeyHex: string } }).relay = {
		pubkeyHex: botPubkey,
		query: async (filter: { kinds?: number[] }) => events[filter.kinds?.[0] ?? 0] ?? [],
	};

	const found = await adapter.searchMessages({
		channelId: CHANNEL,
		queries: ["삭제"],
		limit: 10,
	});

	expect(found.map((candidate) => candidate.messageID)).toEqual(["a".repeat(64)]);
	expect(found[0]?.text).toBe("삭제하지 못했습니다");
});

test("message.search without a resolvable channel is refused", async () => {
	const adapter = adapterAnswering({ 9: [], 40003: [], 9005: [] });
	const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);
	const response = await handler(
		new Request("http://chatd/v1/platform/buzz/message.search", {
			method: "POST",
			body: JSON.stringify({ queries: ["아무거나"] }),
		}),
	);
	expect(response.status).toBe(400);
});
