import { describe, expect, test } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { reactionsTo } from "../src/adapters/buzz/user-reactions.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const MESSAGE = "a".repeat(64);
const GIVER = "b".repeat(64);
const OTHER_GIVER = "c".repeat(64);

function reaction(id: string, pubkey: string, emoji: string): BuzzEvent {
	return { id, pubkey, created_at: 100, kind: 7, tags: [["e", MESSAGE], ["h", CHANNEL]], content: emoji, sig: "" };
}

function takeBack(id: string, reactionID: string): BuzzEvent {
	return {
		id,
		pubkey: GIVER,
		created_at: 200,
		kind: 9005,
		tags: [["h", CHANNEL], ["e", reactionID], ["k", "7"]],
		content: "",
		sig: "",
	};
}

function messageDeletion(id: string, messageID: string): BuzzEvent {
	return { id, pubkey: GIVER, created_at: 200, kind: 9005, tags: [["h", CHANNEL], ["e", messageID]], content: "", sig: "" };
}

function relayHolding(events: BuzzEvent[]) {
	return {
		query: async (filter: { kinds?: number[]; "#e"?: string[]; limit?: number }) =>
			events
				.filter(
					(event) =>
						(!filter.kinds || filter.kinds.includes(event.kind)) &&
						(!filter["#e"] || event.tags.some((tag) => tag[0] === "e" && filter["#e"]?.includes(tag[1] as string))),
				)
				.sort((first, second) => second.created_at - first.created_at)
				.slice(0, filter.limit ?? events.length),
	};
}

function adapterOver(events: BuzzEvent[]) {
	const adapter = new BuzzAdapter({
		relayURL: "wss://relay.test",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
	(adapter as unknown as { relay: unknown }).relay = relayHolding(events);
	return adapter;
}

function messageEvent(): BuzzEvent {
	return { id: MESSAGE, pubkey: GIVER, created_at: 50, kind: 9, tags: [["h", CHANNEL]], content: "먼저 쓴 글", sig: "" };
}

function deletedAmong(adapter: BuzzAdapter, events: BuzzEvent[]): Promise<Set<string>> {
	return (adapter as unknown as { deletedAmong: (read: BuzzEvent[]) => Promise<Set<string>> }).deletedAmong(events);
}

describe("reactions a person took back", () => {
	test("the giver drops out while the others stay", async () => {
		const relay = relayHolding([
			reaction("1".repeat(64), GIVER, "👀"),
			reaction("2".repeat(64), OTHER_GIVER, "👀"),
			takeBack("3".repeat(64), "1".repeat(64)),
		]);

		const reactions = await reactionsTo(relay, [MESSAGE]);

		expect(reactions.get(MESSAGE)?.[0]?.byPubkeyHexes).toEqual([OTHER_GIVER]);
	});

	test("the reaction disappears when its last giver took it back", async () => {
		const relay = relayHolding([reaction("1".repeat(64), GIVER, "👀"), takeBack("3".repeat(64), "1".repeat(64))]);

		const reactions = await reactionsTo(relay, [MESSAGE]);

		expect(reactions.get(MESSAGE) ?? []).toEqual([]);
	});

	test("a take-back of one emoji leaves the person's other emoji alone", async () => {
		const relay = relayHolding([
			reaction("1".repeat(64), GIVER, "👀"),
			reaction("2".repeat(64), GIVER, "🚀"),
			takeBack("3".repeat(64), "1".repeat(64)),
		]);

		const reactions = await reactionsTo(relay, [MESSAGE]);

		expect(reactions.get(MESSAGE)?.map((given) => given.emoji)).toEqual(["🚀"]);
	});

	test("nothing taken back leaves every reaction standing", async () => {
		const relay = relayHolding([reaction("1".repeat(64), GIVER, "👀"), reaction("2".repeat(64), OTHER_GIVER, "👀")]);

		const reactions = await reactionsTo(relay, [MESSAGE]);

		expect(reactions.get(MESSAGE)?.[0]?.byPubkeyHexes).toEqual([GIVER, OTHER_GIVER]);
	});
});

describe("a message deletion and a reaction take-back share one kind", () => {
	test("deleting the message does not hide the reactions on it", async () => {
		const relay = relayHolding([reaction("1".repeat(64), GIVER, "👀"), messageDeletion("4".repeat(64), MESSAGE)]);

		const reactions = await reactionsTo(relay, [MESSAGE]);

		expect(reactions.get(MESSAGE)?.map((given) => given.emoji)).toEqual(["👀"]);
	});

	test("a reaction take-back is not read as the message being deleted", async () => {
		const adapter = adapterOver([reaction("1".repeat(64), GIVER, "👀"), takeBack("3".repeat(64), "1".repeat(64))]);

		const deleted = await deletedAmong(adapter, [messageEvent()]);

		expect([...deleted]).toEqual([]);
	});

	test("take-backs do not crowd a message deletion out of the reader's window", async () => {
		const clicking = Array.from({ length: 200 }, (_unused, turn) => ({
			...takeBack(String(turn).padStart(64, "0"), "1".repeat(64)),
			created_at: 100 + turn,
		}));
		const adapter = adapterOver([{ ...messageDeletion("4".repeat(64), MESSAGE), created_at: 60 }, ...clicking]);

		const deleted = await deletedAmong(adapter, [messageEvent()]);

		expect(deleted.has(MESSAGE)).toBe(true);
	});
});

describe("the agent's own reaction reader", () => {
	test("leaves out a reaction its giver took back", async () => {
		const adapter = new BuzzAdapter({
			relayURL: "wss://relay.test",
			privateKeyHex: "1".repeat(64),
			botDisplayName: "internkim",
		});
		(adapter as unknown as { relay: unknown }).relay = relayHolding([
			reaction("1".repeat(64), GIVER, "👀"),
			reaction("2".repeat(64), OTHER_GIVER, "🚀"),
			takeBack("3".repeat(64), "1".repeat(64)),
		]);

		const summaries = await adapter.fetchReactions(adapter.encodeThreadId({ channelId: CHANNEL }));

		expect(summaries.get(MESSAGE)?.map((given) => given.emoji)).toEqual(["🚀"]);
	});
});
