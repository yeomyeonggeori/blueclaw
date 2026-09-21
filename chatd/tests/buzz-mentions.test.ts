import { describe, expect, test } from "bun:test";
import { mentionsOf, mentionTags } from "../src/adapters/buzz/user-mentions.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const alice = "a".repeat(64);
const bob = "b".repeat(64);

function event(tags: string[][]): BuzzEvent {
	return { id: "event-1", pubkey: alice, created_at: 0, kind: 9, tags, content: "", sig: "" };
}

describe("mentionTags", () => {
	test("names each mentioned person by key", () => {
		expect(mentionTags({ pubkeyHexes: [alice, bob], isEveryone: false })).toEqual([
			["p", alice],
			["p", bob],
		]);
	});

	test("everyone travels as one tag, not as a key per member", () => {
		expect(mentionTags({ pubkeyHexes: [], isEveryone: true })).toEqual([["mention", "everyone"]]);
	});

	test("names a person once however often they were picked", () => {
		expect(mentionTags({ pubkeyHexes: [alice, alice], isEveryone: false })).toEqual([["p", alice]]);
	});

	test("a message that calls on nobody carries no mention tag", () => {
		expect(mentionTags(undefined)).toEqual([]);
		expect(mentionTags({ pubkeyHexes: [], isEveryone: false })).toEqual([]);
	});
});

describe("mentionsOf", () => {
	test("reads back what was written", () => {
		const tags = mentionTags({ pubkeyHexes: [alice, bob], isEveryone: true });
		expect(mentionsOf(event(tags))).toEqual({ pubkeyHexes: [alice, bob], isEveryone: true });
	});

	test("a message with no mention tag calls on nobody", () => {
		expect(mentionsOf(event([["h", "channel-1"]]))).toEqual({ pubkeyHexes: [], isEveryone: false });
	});

	test("leaves the thread and media tags alone", () => {
		const tags = [["h", "channel-1"], ["e", "root-1", "", "root"], ["p", alice]];
		expect(mentionsOf(event(tags))).toEqual({ pubkeyHexes: [alice], isEveryone: false });
	});
});
