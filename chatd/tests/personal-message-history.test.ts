import { expect, test } from "bun:test";
import { channelMessagePageOf } from "../src/adapters/buzz/user-session.ts";
import { DELETE_MESSAGE_KIND } from "../src/adapters/buzz/deletions.ts";
import { EDIT_MESSAGE_KIND, STREAM_MESSAGE_KIND, type BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL = "channel";

function messageAt(index: number): BuzzEvent {
	return {
		id: index.toString(16).padStart(64, "0"),
		pubkey: "c".repeat(64),
		created_at: 1784900000 + index,
		kind: STREAM_MESSAGE_KIND,
		tags: [["h", CHANNEL]],
		content: `message ${index}`,
		sig: "",
	};
}

type Filter = { ids?: string[]; kinds?: number[]; until?: number; limit?: number; "#e"?: string[] };

function relayHolding(messages: BuzzEvent[], deletions: BuzzEvent[] = [], edits: BuzzEvent[] = []) {
	return {
		query: async (filter: object) => {
			const wanted = filter as Filter;
			if (wanted.ids) return messages.filter((message) => wanted.ids?.includes(message.id));
			if (wanted.kinds?.includes(DELETE_MESSAGE_KIND)) {
				return deletions.filter((deletion) => wanted["#e"]?.includes(deletion.tags[0]?.[1] ?? ""));
			}
			if (wanted.kinds?.includes(EDIT_MESSAGE_KIND)) {
				return edits.filter((edit) => edit.tags.some((tag) => tag[0] === "e" && wanted["#e"]?.includes(tag[1] ?? "")));
			}
			if (!wanted.kinds?.includes(STREAM_MESSAGE_KIND)) return [];
			const until = wanted.until ?? Number.POSITIVE_INFINITY;
			return messages
				.filter((message) => message.created_at <= until)
				.sort((first, second) => second.created_at - first.created_at)
				.slice(0, wanted.limit);
		},
	};
}

test("an older page continues from the message the reader already has", async () => {
	const messages = Array.from({ length: 5 }, (_, index) => messageAt(index));
	const relay = relayHolding(messages);

	const newest = await channelMessagePageOf(relay, CHANNEL, 2);
	const older = await channelMessagePageOf(relay, CHANNEL, 2, newest.messages[0]?.id);

	expect(newest.messages.map((message) => message.body)).toEqual(["message 3", "message 4"]);
	expect(older.messages.map((message) => message.body)).toEqual(["message 2"]);
	expect(older.hasMoreBefore).toBe(true);
});

test("a deleted message on a full page does not end history", async () => {
	const messages = Array.from({ length: 4 }, (_, index) => messageAt(index));
	const deletion: BuzzEvent = { ...messageAt(99), kind: DELETE_MESSAGE_KIND, tags: [["e", messageAt(3).id]] };
	const page = await channelMessagePageOf(relayHolding(messages, [deletion]), CHANNEL, 2);

	expect(page.messages.map((message) => message.body)).toEqual(["message 2"]);
	expect(page.hasMoreBefore).toBe(true);
});

test("the oldest page says nothing is older", async () => {
	const page = await channelMessagePageOf(relayHolding([messageAt(0)]), CHANNEL, 2);

	expect(page.hasMoreBefore).toBe(false);
});

test("a cursor the conversation never returned is refused", async () => {
	await expect(channelMessagePageOf(relayHolding([messageAt(0)]), CHANNEL, 2, "f".repeat(64))).rejects.toThrow(
		"is not in conversation",
	);
});

test("an edited message calls on the people its latest edit names", async () => {
	const original: BuzzEvent = { ...messageAt(0), tags: [["h", CHANNEL], ["p", "a".repeat(64)]] };
	const edit: BuzzEvent = {
		...messageAt(1),
		kind: EDIT_MESSAGE_KIND,
		tags: [["h", CHANNEL], ["e", original.id], ["p", "b".repeat(64)]],
		content: "edited",
	};
	const page = await channelMessagePageOf(relayHolding([original], [], [edit]), CHANNEL, 2);

	expect(page.messages[0]?.body).toBe("edited");
	expect(page.messages[0]?.mentions).toEqual({ pubkeyHexes: ["b".repeat(64)], isEveryone: false });
});

const AGENT = "d".repeat(64);

function questionFromTheAgent(): BuzzEvent {
	return {
		...messageAt(0),
		pubkey: AGENT,
		tags: [
			["h", CHANNEL],
			["ask", "interaction-1", "approval", "c".repeat(64)],
			["option", "approve", "승인", "recommended"],
			["option", "deny", "거절"],
		],
		content: "할까요?\n\n1. 승인\n2. 거절",
	};
}

function resolutionBy(author: string, index: number, resolvedTag: string[]): BuzzEvent {
	return {
		...messageAt(index),
		pubkey: author,
		kind: EDIT_MESSAGE_KIND,
		tags: [["h", CHANNEL], ["e", questionFromTheAgent().id], resolvedTag],
		content: questionFromTheAgent().content,
	};
}

test("a question reads back with the choices its tags carry", async () => {
	const page = await channelMessagePageOf(relayHolding([questionFromTheAgent()]), CHANNEL, 2);

	expect(page.messages[0]?.ask).toEqual({
		interactionID: "interaction-1",
		kind: "approval",
		targetExternalID: "c".repeat(64),
		options: [
			{ key: "approve", label: "승인", isRecommended: true },
			{ key: "deny", label: "거절", isRecommended: false },
		],
	});
	expect(page.messages[0]?.isFailure).toBeUndefined();
});

test("a question its author recorded as answered reads back resolved", async () => {
	const chosen = resolutionBy(AGENT, 1, ["resolved", "interaction-1", "deny"]);
	const ownWords = resolutionBy(AGENT, 2, ["resolved", "interaction-1"]);

	const toOption = await channelMessagePageOf(relayHolding([questionFromTheAgent()], [], [chosen]), CHANNEL, 2);
	const inOwnWords = await channelMessagePageOf(relayHolding([questionFromTheAgent()], [], [chosen, ownWords]), CHANNEL, 2);

	expect(toOption.messages[0]?.ask?.resolution).toEqual({ key: "deny" });
	expect(inOwnWords.messages[0]?.ask?.resolution).toEqual({});
});

test("a resolution written by anyone but the question's author is ignored", async () => {
	const forged = resolutionBy("c".repeat(64), 1, ["resolved", "interaction-1", "approve"]);

	const page = await channelMessagePageOf(relayHolding([questionFromTheAgent()], [], [forged]), CHANNEL, 2);

	expect(page.messages[0]?.ask).toBeDefined();
	expect(page.messages[0]?.ask?.resolution).toBeUndefined();
});

test("a failure notice reads back as one", async () => {
	const failure: BuzzEvent = { ...messageAt(0), tags: [["h", CHANNEL], ["reply-kind", "error"]] };

	const page = await channelMessagePageOf(relayHolding([failure, messageAt(1)]), CHANNEL, 2);

	expect(page.messages.map((message) => message.isFailure)).toEqual([true, undefined]);
});

test("recording a resolution leaves the question's words and mentions as they were", async () => {
	const named = "e".repeat(64);
	const question: BuzzEvent = { ...questionFromTheAgent(), tags: [...questionFromTheAgent().tags, ["p", named]] };
	const correction: BuzzEvent = {
		...messageAt(1),
		pubkey: AGENT,
		kind: EDIT_MESSAGE_KIND,
		tags: [["h", CHANNEL], ["e", question.id], ["p", named]],
		content: "고쳐 쓴 질문",
	};
	const resolution = resolutionBy(AGENT, 2, ["resolved", "interaction-1", "approve"]);

	const page = await channelMessagePageOf(relayHolding([question], [], [correction, resolution]), CHANNEL, 2);

	expect(page.messages[0]?.body).toBe("고쳐 쓴 질문");
	expect(page.messages[0]?.mentions).toEqual({ pubkeyHexes: [named], isEveryone: false });
	expect(page.messages[0]?.ask?.resolution).toEqual({ key: "approve" });
});

test("an approval's options read back by key even though they carry no label", async () => {
	const approval: BuzzEvent = {
		...messageAt(0),
		pubkey: AGENT,
		tags: [["h", CHANNEL], ["ask", "interaction-1", "approval", ""], ["option", "approve", ""], ["option", "reject", ""]],
	};

	const page = await channelMessagePageOf(relayHolding([approval]), CHANNEL, 2);

	expect(page.messages[0]?.ask?.options).toEqual([
		{ key: "approve", label: "", isRecommended: false },
		{ key: "reject", label: "", isRecommended: false },
	]);
});
