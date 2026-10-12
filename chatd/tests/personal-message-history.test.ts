import { expect, test } from "bun:test";
import { channelMessagePageOf } from "../src/adapters/buzz/user-session.ts";
import { DELETE_MESSAGE_KIND } from "../src/adapters/buzz/deletions.ts";
import { STREAM_MESSAGE_KIND, type BuzzEvent } from "../src/adapters/buzz/types.ts";

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

function relayHolding(messages: BuzzEvent[], deletions: BuzzEvent[] = []) {
	return {
		query: async (filter: object) => {
			const wanted = filter as Filter;
			if (wanted.ids) return messages.filter((message) => wanted.ids?.includes(message.id));
			if (wanted.kinds?.includes(DELETE_MESSAGE_KIND)) {
				return deletions.filter((deletion) => wanted["#e"]?.includes(deletion.tags[0]?.[1] ?? ""));
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
