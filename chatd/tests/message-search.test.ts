import { afterEach, beforeEach, describe, expect, test } from "bun:test";

import {
	searchScore,
	rankBySearchScore,
	newestVerbatimMatches,
	messageSearchMostResults,
	messageSearchScanLimit,
} from "../src/message-search";
import type { ChatdConfiguration } from "../src/configuration";
import type { BuzzEvent } from "../src/adapters/buzz/types";
import { replaceRelayClientInThisFile } from "./relay-client-replacement.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const BOT_SECRET = "1".repeat(64);
const REQUESTER_SECRET = "2".repeat(64);
const ADMIND_BASE_URL = "http://127.0.0.1:18080";

type Shown = (filter: { kinds?: number[]; ids?: string[] }) => Promise<unknown[]>;
type AgentRelay = { relay: { query: Shown; pubkeyHex: string } };

let shownToTheRequester: Shown = async () => [];

replaceRelayClientInThisFile((_relayURL: string, secretHex: string) => ({
	pubkeyHex: pubkeyFromSecret(secretHex),
	connect: async () => {},
	connectUnlessRestricted: async () => {},
	disconnect: () => {},
	subscribe: () => {},
	query: (filter: { kinds?: number[]; ids?: string[] }) => shownToTheRequester(filter),
}));

const { BuzzAdapter } = await import("../src/adapters/buzz/adapter");
const { pubkeyFromSecret } = await import("../src/adapters/buzz/user-session");
const { RelayQueryIncomplete } = await import("../src/adapters/buzz/relay-connection");
const { createOutboundHandler, readerOfASearch } = await import("../src/outbound");

const REQUESTER = pubkeyFromSecret(REQUESTER_SECRET);
const configuration = {
	admindBaseURL: ADMIND_BASE_URL,
	buzz: { relayURL: "ws://localhost:3000", privateKeyHex: BOT_SECRET },
} as ChatdConfiguration;

const realFetch = globalThis.fetch;

beforeEach(() => {
	shownToTheRequester = async () => [];
	globalThis.fetch = (async (input: Request | string | URL, init?: RequestInit) => {
		const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
		if (!url.startsWith(ADMIND_BASE_URL)) return await realFetch(input as Request, init);
		return Response.json({ secretHex: REQUESTER_SECRET });
	}) as typeof fetch;
});

afterEach(() => {
	globalThis.fetch = realFetch;
});

function shownToBoth(adapter: InstanceType<typeof BuzzAdapter>, shown: Shown): void {
	(adapter as unknown as AgentRelay).relay = { pubkeyHex: adapter.botPubkey ?? "", query: shown };
	shownToTheRequester = shown;
}

function searched(adapter: InstanceType<typeof BuzzAdapter>, search: object): Promise<Response> {
	const handler = createOutboundHandler({ buzz: adapter }, configuration);
	return handler(
		new Request("http://chatd/v1/platform/buzz/message.search", {
			method: "POST",
			body: JSON.stringify({ requesterPubkeyHex: REQUESTER, ...search }),
		}),
	);
}

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

function adapterAnswering(events: Record<number, unknown[]>): InstanceType<typeof BuzzAdapter> {
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
	const botPubkey = adapter.botPubkey ?? "";
	const events: Record<number, unknown[]> = {
		9: [
			messageEvent("a".repeat(64), botPubkey, "오늘 점심 공지: 상하이 미팅 결과를 공유합니다", 100),
			messageEvent("b".repeat(64), REQUESTER, "상하이 미팅은 어땠나요?", 101),
			messageEvent("c".repeat(64), botPubkey, "날씨가 좋네요", 102),
		],
	};
	shownToBoth(adapter, async (filter) => events[filter.kinds?.[0] ?? 0] ?? []);

	const response = await searched(adapter, { channelID: CHANNEL, authoredBy: "assistant", queries: ["상하이 미팅"] });

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
	const botPubkey = adapter.botPubkey ?? "";
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
	};
	shownToBoth(adapter, async (filter) => events[filter.kinds?.[0] ?? 0] ?? []);

	const found = await adapter.searchMessages({ channelId: CHANNEL, queries: ["삭제"], limit: 10 }, adapter.reader);

	expect(found.candidates.map((candidate) => candidate.messageID)).toEqual(["a".repeat(64)]);
	expect(found.candidates[0]?.text).toBe("삭제하지 못했습니다");
});

type RelayFilter = { kinds?: number[]; ids?: string[]; limit?: number; until?: number };
type SearchPage = { candidates: { messageID: string }[]; nextCursor?: string };

function relayHolding(messages: ReturnType<typeof messageEvent>[]): Shown {
	return async (filter: RelayFilter) => {
		if (filter.kinds?.[0] !== 9) return [];
		return messages
			.filter((message) => filter.until === undefined || message.created_at <= filter.until)
			.sort((first, second) => second.created_at - first.created_at)
			.slice(0, filter.limit);
	};
}

async function everyPage(adapter: InstanceType<typeof BuzzAdapter>, search: object): Promise<string[][]> {
	const pages: string[][] = [];
	let cursor: string | undefined;
	do {
		const response = await searched(adapter, { channelID: CHANNEL, ...search, cursor });
		expect(response.status).toBe(200);
		const page = (await response.json()) as SearchPage;
		pages.push(page.candidates.map((candidate) => candidate.messageID));
		cursor = page.nextCursor;
	} while (cursor && pages.length < 100);
	return pages;
}

function numbered(count: number, textOf: (index: number) => string, secondOf: (index: number) => number) {
	return Array.from({ length: count }, (_, index) =>
		messageEvent(index.toString(16).padStart(64, "0"), REQUESTER, textOf(index), secondOf(index)),
	);
}

test("message.search pages through more matches than one answer holds", async () => {
	const adapter = adapterAnswering({});
	const messages = numbered(7, (index) => `회의록 ${index}`, (index) => 100 + index);
	shownToBoth(adapter, relayHolding(messages));

	const pages = await everyPage(adapter, { limit: 3 });

	expect(pages.map((page) => page.length)).toEqual([3, 3, 1]);
	expect(pages.flat()).toEqual(messages.map((message) => message.id).reverse());
});

test("message.search pages keep their place when somebody writes meanwhile", async () => {
	const adapter = adapterAnswering({});
	const messages = numbered(4, (index) => `회의록 ${index}`, (index) => 100 + index);
	shownToBoth(adapter, relayHolding(messages));

	const first = (await (await searched(adapter, { channelID: CHANNEL, limit: 2 })).json()) as SearchPage;
	messages.push(messageEvent("f".repeat(64), REQUESTER, "방금 쓴 글", 200));
	const second = (await (
		await searched(adapter, { channelID: CHANNEL, limit: 2, cursor: first.nextCursor })
	).json()) as SearchPage;

	const answered = [...first.candidates, ...second.candidates].map((candidate) => candidate.messageID);
	expect(answered).toEqual(messages.slice(0, 4).map((message) => message.id).reverse());
	expect(second.nextCursor).toBeUndefined();
});

test("message.search reads past the window it scans, losing nothing written in the same second", async () => {
	const adapter = adapterAnswering({});
	const messages = numbered(messageSearchScanLimit + 40, (index) => `글 ${index}`, (index) => 1000 + Math.floor(index / 3));
	shownToBoth(adapter, relayHolding(messages));

	const pages = await everyPage(adapter, { limit: 50 });

	const answered = pages.flat();
	expect(new Set(answered).size).toBe(answered.length);
	expect(new Set(answered)).toEqual(new Set(messages.map((message) => message.id)));
});

test("message.search finds a match older than the first window", async () => {
	const adapter = adapterAnswering({});
	const messages = numbered(messageSearchScanLimit + 10, (index) => (index === 2 ? "상하이 미팅 결과" : `글 ${index}`), (index) => 1000 + index);
	shownToBoth(adapter, relayHolding(messages));

	const pages = await everyPage(adapter, { queries: ["상하이 미팅"], limit: 5 });

	expect(pages[0]).toEqual([]);
	expect(pages.flat()).toEqual([messages[2]?.id ?? ""]);
});

test("message.search refuses a cursor it never answered with", async () => {
	const adapter = adapterAnswering({});
	shownToBoth(adapter, relayHolding([]));

	const response = await searched(adapter, { channelID: CHANNEL, cursor: "page-two" });

	expect(response.status).toBe(400);
});

test("message.search without a conversation or without a person asking is refused", async () => {
	const adapter = adapterAnswering({ 9: [], 40003: [], 9005: [] });

	expect((await searched(adapter, { queries: ["아무거나"] })).status).toBe(400);
	expect((await searched(adapter, { channelID: CHANNEL, requesterPubkeyHex: undefined })).status).toBe(400);
});

test("reading by ID crosses channels and needs no channel named", async () => {
	const adapter = adapterAnswering({ 9: [], 40003: [], 9005: [] });
	const otherChannel = "71031c16-5569-8595-06ea-1e3caf3dd83f";
	const targetID = "a".repeat(64);
	const target = { id: targetID, pubkey: adapter.botPubkey, created_at: 100, kind: 9, tags: [["h", otherChannel]], content: "잡담에 올라간 글", sig: "" };
	shownToBoth(adapter, async (filter) => (filter.ids ? [target] : []));

	const response = await searched(adapter, { messageIDs: [targetID] });

	expect(response.status).toBe(200);
	const body = (await response.json()) as { channelID: string; candidates: { messageID: string; channelID: string; text: string }[] };
	expect(body.candidates[0]?.messageID).toBe(targetID);
	expect(body.candidates[0]?.channelID).toBe(otherChannel);
	expect(body.candidates[0]?.text).toBe("잡담에 올라간 글");
});

describe("a search is answered with what everyone who sees the answer may read", () => {
	const saidInPrivate = messageEvent("a".repeat(64), "e".repeat(64), "둘만 나눈 이야기", 100) as BuzzEvent;
	const saidInTheOpen = messageEvent("b".repeat(64), "e".repeat(64), "모두에게 한 이야기", 101) as BuzzEvent;
	const showing = (...events: BuzzEvent[]) => ({ query: async () => events });
	const refusing = { query: async () => Promise.reject(new RelayQueryIncomplete("ws://relay", "restricted: not a channel member")) };
	const idsRead = async (reader: { query: (filter: object) => Promise<BuzzEvent[]> }) =>
		(await reader.query({ kinds: [9] })).map((event) => event.id);

	test("through the agent, a message only one of the two is shown is left out", async () => {
		const onlyTheRequesterSeesMore = readerOfASearch(showing(saidInTheOpen), showing(saidInPrivate, saidInTheOpen), false);
		const onlyTheAgentSeesMore = readerOfASearch(showing(saidInPrivate, saidInTheOpen), showing(saidInTheOpen), false);

		expect(await idsRead(onlyTheRequesterSeesMore)).toEqual([saidInTheOpen.id]);
		expect(await idsRead(onlyTheAgentSeesMore)).toEqual([saidInTheOpen.id]);
	});

	test("on the person's own call, what the agent is shown does not matter", async () => {
		const reader = readerOfASearch(refusing, showing(saidInPrivate), true);

		expect(await idsRead(reader)).toEqual([saidInPrivate.id]);
	});

	test("a conversation the relay refuses either of them is refused, naming who", async () => {
		const agentRefused = readerOfASearch(refusing, showing(saidInPrivate), false);
		const requesterRefused = readerOfASearch(showing(saidInPrivate), refusing, false);

		expect(idsRead(agentRefused)).rejects.toThrow("the agent may not read this conversation");
		expect(idsRead(requesterRefused)).rejects.toThrow("the person who asked may not read this conversation");
	});

	test("the refusal reaches the caller as a 403", async () => {
		const adapter = adapterAnswering({ 9: [], 40003: [], 9005: [] });
		shownToTheRequester = refusing.query;

		expect((await searched(adapter, { channelID: CHANNEL })).status).toBe(403);
	});
});

describe("a person searching their own conversation", () => {
	const textOf = (message: { text: string }) => message.text;
	const createdAtOf = (message: { at: number }) => message.at;

	test("is shown only messages that hold the words, newest first", () => {
		const found = newestVerbatimMatches(
			[
				{ id: "old", text: "지난주 회의록 공유합니다", at: 1 },
				{ id: "near-miss", text: "회의 끝나고 기록할게요", at: 2 },
				{ id: "new", text: "회의록은 제가 쓸게요", at: 3 },
			],
			"회의록",
			textOf,
			createdAtOf,
		);

		expect(found.map((message) => message.id)).toEqual(["new", "old"]);
	});

	test("matches whatever case and spacing the words were typed in", () => {
		const found = newestVerbatimMatches([{ id: "one", text: "Weekly  Report is up", at: 1 }], "weekly report", textOf, createdAtOf);

		expect(found.map((message) => message.id)).toEqual(["one"]);
	});

	test("hands back no more than the most a search returns", () => {
		const many = Array.from({ length: messageSearchMostResults + 5 }, (_, index) => ({ id: String(index), text: "회의록", at: index }));

		expect(newestVerbatimMatches(many, "회의록", textOf, createdAtOf)).toHaveLength(messageSearchMostResults);
	});
});
