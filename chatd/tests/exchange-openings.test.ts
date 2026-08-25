import { expect, test } from "bun:test";

import { buildVisibleContext } from "../src/visible-context";

type Raw = { id: string; rootId?: string };

function messageOf(id: string, text: string, rootId?: string) {
	return {
		id,
		text,
		author: { userId: "u-1", userName: "이동하", fullName: "이동하" },
		metadata: { dateSent: new Date("2026-08-25T07:13:00Z") },
		raw: { id, rootId } as Raw,
	};
}

function adapterHolding(messages: ReturnType<typeof messageOf>[]) {
	return {
		name: "buzz",
		fetchMessages: async () => ({ messages }),
		fetchThread: async () => ({ channelId: "c-1", isDM: false }),
		getUser: async () => null,
		threadRootIdOf: (raw: unknown) => (raw as Raw).rootId ?? (raw as Raw).id,
	} as never;
}

const openingOne = messageOf("m-1", "NVIDIA·젯슨 공급 미팅 지워주고");
const openingTwo = messageOf("m-2", "목요일 팁스 연구노트 작성 일정 추가해줘");
const replyInTwo = messageOf("m-3", "등록했습니다", "m-2");

// A message that starts its own exchange is read against what the other
// exchanges opened with. What was said inside them belongs to whoever is in
// them, and read here it answered one request with another's subject.
test("a new exchange reads the other openings and not what was said inside them", async () => {
	const context = await buildVisibleContext(adapterHolding([openingOne, openingTwo, replyInTwo]), "buzz:c-1", {
		onlyExchangeOpenings: true,
	});

	expect(context.messages.map((message) => message.text)).toEqual([openingOne.text, openingTwo.text]);
});

test("a reply reads everything its own exchange holds", async () => {
	const context = await buildVisibleContext(adapterHolding([openingTwo, replyInTwo]), "buzz:c-1:m-2");

	expect(context.messages.map((message) => message.text)).toEqual([openingTwo.text, replyInTwo.text]);
});
