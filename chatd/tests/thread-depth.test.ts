import { expect, test } from "bun:test";
import { threadRootOf } from "../src/adapters/buzz/user-session.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const ROOT = "a".repeat(64);
const REPLY = "b".repeat(64);

function event(id: string, tags: string[][]): BuzzEvent {
	return { id, pubkey: "c".repeat(64), created_at: 1784900000, kind: 9, tags, content: "", sig: "" };
}

function relayHolding(events: BuzzEvent[]) {
	return {
		query: async (filter: { ids?: string[] }) => events.filter((held) => (filter.ids ?? []).includes(held.id)),
	};
}

test("answering a reply answers what that reply answered", async () => {
	const relay = relayHolding([
		event(ROOT, [["h", "channel-1"]]),
		event(REPLY, [["h", "channel-1"], ["e", ROOT, "", "root"], ["e", ROOT, "", "reply"]]),
	]);

	expect(await threadRootOf(relay, REPLY)).toBe(ROOT);
});

test("answering a message that started a conversation answers it", async () => {
	const relay = relayHolding([event(ROOT, [["h", "channel-1"]])]);

	expect(await threadRootOf(relay, ROOT)).toBe(ROOT);
});

test("answering nothing names no conversation", async () => {
	expect(await threadRootOf(relayHolding([]), undefined)).toBeUndefined();
});

test("a message the relay no longer holds is answered as itself", async () => {
	expect(await threadRootOf(relayHolding([]), REPLY)).toBe(REPLY);
});
