import { expect, test } from "bun:test";
import { deletionsTo } from "../src/adapters/buzz/user-session.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const KEPT = "a".repeat(64);
const TAKEN_BACK = "b".repeat(64);

function deletionOf(messageID: string): BuzzEvent {
	return {
		id: "d".repeat(64),
		pubkey: "c".repeat(64),
		created_at: 1784900001,
		kind: 9005,
		tags: [["e", messageID]],
		content: "",
		sig: "",
	};
}

function relayAnswering(events: BuzzEvent[]) {
	return { query: async () => events };
}

// The agent's reader has always honoured a deletion; the reader a person's
// messenger screen uses had not, so a message somebody deleted went on being
// shown to everybody. Same messenger, two readers, one of them behind.
test("a message its author took back is named as taken back", async () => {
	const taken = await deletionsTo(relayAnswering([deletionOf(TAKEN_BACK)]), [KEPT, TAKEN_BACK]);

	expect(taken.has(TAKEN_BACK)).toBe(true);
	expect(taken.has(KEPT)).toBe(false);
});

test("nothing deleted names nothing", async () => {
	expect((await deletionsTo(relayAnswering([]), [KEPT])).size).toBe(0);
});

test("no messages asks the relay nothing", async () => {
	let asked = false;
	const relay = {
		query: async () => {
			asked = true;
			return [];
		},
	};

	expect((await deletionsTo(relay, [])).size).toBe(0);
	expect(asked).toBe(false);
});
