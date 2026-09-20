import { describe, expect, test } from "bun:test";
import { ThreadPartlyDeleted, deleteThread, repliesUnder } from "../src/adapters/buzz/thread-deletion.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL = "6955ae67-a6d5-47c7-83b6-aea4902c20f0";
const ROOT = "a".repeat(64);
const AUTHOR = "b".repeat(64);
const ANSWERER = "c".repeat(64);

function root(): BuzzEvent {
	return { id: ROOT, pubkey: AUTHOR, created_at: 100, kind: 9, tags: [["h", CHANNEL]], content: "먼저 쓴 글", sig: "" };
}

function reply(id: string, pubkey: string, at: number, rootID = ROOT): BuzzEvent {
	return {
		id,
		pubkey,
		created_at: at,
		kind: 9,
		tags: [["h", CHANNEL], ["e", rootID, "", "root"], ["e", rootID, "", "reply"]],
		content: "답글",
		sig: "",
	};
}

function relayHolding(events: BuzzEvent[], complete = true) {
	return {
		queryComplete: async (filter: { kinds?: number[]; "#e"?: string[]; "#h"?: string[]; limit?: number }) => ({
			events: events
				.filter(
					(event) =>
						(!filter.kinds || filter.kinds.includes(event.kind)) &&
						(!filter["#h"] || event.tags.some((tag) => tag[0] === "h" && filter["#h"]?.includes(tag[1] as string))) &&
						(!filter["#e"] || event.tags.some((tag) => tag[0] === "e" && filter["#e"]?.includes(tag[1] as string))),
				)
				.slice(0, filter.limit ?? events.length),
			complete,
		}),
	};
}

describe("the replies a thread holds", () => {
	test("are the messages naming the root, oldest first", async () => {
		const relay = relayHolding([root(), reply("2".repeat(64), ANSWERER, 300), reply("1".repeat(64), AUTHOR, 200)]);

		const { replies } = await repliesUnder(relay, CHANNEL, ROOT);

		expect(replies.map((event) => event.id)).toEqual(["1".repeat(64), "2".repeat(64)]);
	});

	test("never include the root itself", async () => {
		const relay = relayHolding([root()]);

		expect((await repliesUnder(relay, CHANNEL, ROOT)).replies).toEqual([]);
	});

	test("leave out a message hanging under a different root", async () => {
		const relay = relayHolding([reply("1".repeat(64), ANSWERER, 200, "f".repeat(64))]);

		expect((await repliesUnder(relay, CHANNEL, ROOT)).replies).toEqual([]);
	});
});

describe("deleting a thread", () => {
	test("takes the replies with the root", async () => {
		const relay = relayHolding([root(), reply("1".repeat(64), ANSWERER, 200), reply("2".repeat(64), AUTHOR, 300)]);
		const gone: string[] = [];

		await deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => true,
			deleteRoot: async () => void gone.push(ROOT),
			deleteReply: async (event) => void gone.push(event.id),
		});

		expect(gone).toEqual([ROOT, "1".repeat(64), "2".repeat(64)]);
	});

	test("leaves every reply standing when the relay refuses the root", async () => {
		const relay = relayHolding([root(), reply("1".repeat(64), ANSWERER, 200)]);
		const gone: string[] = [];

		const refused = deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => true,
			deleteRoot: async () => {
				throw new Error("relay refused this deletion");
			},
			deleteReply: async (event) => void gone.push(event.id),
		});

		await expect(refused).rejects.toThrow("relay refused this deletion");
		expect(gone).toEqual([]);
	});

	test("asks for no reply deletion when the message answers nobody", async () => {
		const relay = relayHolding([root()]);
		const gone: string[] = [];

		await deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => true,
			deleteRoot: async () => void gone.push(ROOT),
			deleteReply: async (event) => void gone.push(event.id),
		});

		expect(gone).toEqual([ROOT]);
	});

	test("names each reply's own author so each deletion can be signed as them", async () => {
		const relay = relayHolding([root(), reply("1".repeat(64), ANSWERER, 200), reply("2".repeat(64), AUTHOR, 300)]);
		const signedAs: string[] = [];

		await deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => true,
			deleteRoot: async () => {},
			deleteReply: async (event) => void signedAs.push(event.pubkey),
		});

		expect(signedAs).toEqual([ANSWERER, AUTHOR]);
	});
});

describe("who may clear the replies", () => {
	test("a deletion nobody judged leaves every reply standing, and says so", async () => {
		const relay = relayHolding([root(), reply("1".repeat(64), ANSWERER, 200)]);
		const gone: string[] = [];

		const partly = deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => false,
			deleteRoot: async () => void gone.push(ROOT),
			deleteReply: async (event) => void gone.push(event.id),
		});

		await expect(partly).rejects.toBeInstanceOf(ThreadPartlyDeleted);
		expect(gone).toEqual([ROOT]);
	});

	test("a message nobody answered is no trouble even when nobody judged it", async () => {
		const relay = relayHolding([root()]);
		const gone: string[] = [];

		await deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => false,
			deleteRoot: async () => void gone.push(ROOT),
			deleteReply: async (event) => void gone.push(event.id),
		});

		expect(gone).toEqual([ROOT]);
	});

	test("is asked only once the root is already gone", async () => {
		const relay = relayHolding([root(), reply("1".repeat(64), ANSWERER, 200)]);
		const order: string[] = [];

		await deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => {
				order.push("asked");
				return true;
			},
			deleteRoot: async () => void order.push("root"),
			deleteReply: async () => void order.push("reply"),
		});

		expect(order).toEqual(["root", "asked", "reply"]);
	});
});

describe("a reply that would not go", () => {
	test("does not stop the others, and is said out loud", async () => {
		const relay = relayHolding([root(), reply("1".repeat(64), ANSWERER, 200), reply("2".repeat(64), AUTHOR, 300)]);
		const gone: string[] = [];

		const partly = deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => true,
			deleteRoot: async () => {},
			deleteReply: async (event) => {
				if (event.id === "1".repeat(64)) throw new Error("the relay would not take it");
				gone.push(event.id);
			},
		});

		await expect(partly).rejects.toBeInstanceOf(ThreadPartlyDeleted);
		expect(gone).toEqual(["2".repeat(64)]);
	});
});

describe("a thread with more replies than one answer can hold", () => {
	test("is said to be more than what came back", async () => {
		const many = Array.from({ length: 500 }, (_unused, turn) =>
			reply(String(turn).padStart(64, "0"), ANSWERER, 200 + turn),
		);
		const relay = relayHolding([root(), ...many]);

		const { replies, wholeThread } = await repliesUnder(relay, CHANNEL, ROOT);

		expect(replies).toHaveLength(500);
		expect(wholeThread).toBe(false);
	});

	test("leaves the caller told rather than quietly short", async () => {
		const many = Array.from({ length: 500 }, (_unused, turn) =>
			reply(String(turn).padStart(64, "0"), ANSWERER, 200 + turn),
		);
		const relay = relayHolding([root(), ...many]);

		const partly = deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => true,
			deleteRoot: async () => {},
			deleteReply: async () => {},
		});

		await expect(partly).rejects.toBeInstanceOf(ThreadPartlyDeleted);
	});
});

describe("a relay that stopped answering", () => {
	test("is not read as a thread nobody replied to", async () => {
		const relay = relayHolding([root(), reply("1".repeat(64), ANSWERER, 200)], false);

		const { wholeThread } = await repliesUnder(relay, CHANNEL, ROOT);

		expect(wholeThread).toBe(false);
	});

	test("leaves the caller told that the thread may not have gone", async () => {
		const relay = relayHolding([root()], false);

		const partly = deleteThread({
			relay,
			channelID: CHANNEL,
			rootEventId: ROOT,
			mayClearReplies: async () => true,
			deleteRoot: async () => {},
			deleteReply: async () => {},
		});

		await expect(partly).rejects.toBeInstanceOf(ThreadPartlyDeleted);
	});
});
