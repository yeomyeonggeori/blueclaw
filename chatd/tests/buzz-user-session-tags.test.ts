import { describe, expect, test } from "bun:test";
import { imetaTag } from "../src/adapters/buzz/blossom.ts";
import { attachmentsOfTags, channelMessageTags } from "../src/adapters/buzz/user-session.ts";
import { carriesTag, type BuzzEvent } from "../src/adapters/buzz/types.ts";

describe("channelMessageTags", () => {
	// resolve_nip10_thread_meta reads the root and reply markers as a pair and
	// returns nothing for an event that carries only one, which leaves the message
	// out of the thread it answers.
	test("a reply names the root as both its root and its parent", () => {
		expect(channelMessageTags("channel-1", [], undefined, "root-1")).toEqual([
			["h", "channel-1"],
			["e", "root-1", "", "root"],
			["e", "root-1", "", "reply"],
		]);
	});

	test("a message that answers nothing carries no thread tag", () => {
		expect(channelMessageTags("channel-1", [], undefined, undefined)).toEqual([["h", "channel-1"]]);
	});

	test("keeps the media and extra tags it was given, in that order", () => {
		const imeta = ["imeta", "url https://example.com/a.png"];
		const mention = ["p", "a".repeat(64)];
		expect(channelMessageTags("channel-1", [imeta], [mention], undefined)).toEqual([
			["h", "channel-1"],
			imeta,
			mention,
		]);
	});
});

describe("the name a file was sent under survives the tag", () => {
	const blob = {
		url: "http://localhost:3000/9f2c.pdf",
		sha256: "9f2c",
		size: 2048,
		mimeType: "application/pdf",
	};

	test("a named file comes back under its own name", () => {
		expect(attachmentsOfTags([imetaTag(blob, "2026 예산.pdf")])).toEqual([
			{
				url: blob.url,
				contentType: "application/pdf",
				sizeBytes: 2048,
				filename: "2026 예산.pdf",
				digest: "9f2c",
			},
		]);
	});

	test("an unnamed file falls back to what the address ends in", () => {
		expect(attachmentsOfTags([imetaTag(blob)]).map((file) => file.filename)).toEqual(["9f2c.pdf"]);
	});

	test("a tag that names no address describes nothing", () => {
		expect(attachmentsOfTags([["imeta", "m image/png"]])).toEqual([]);
	});

	test("a tag that names no hash describes a file no copy can be recognised of", () => {
		const unhashed = ["imeta", "url http://localhost:3000/a.pdf", "m application/pdf"];
		expect(attachmentsOfTags([unhashed]).map((file) => file.digest)).toEqual([""]);
	});
});

// The relay marks a room's visibility with a bare tag on its kind 39000, so a
// value lookup finds nothing where the tag is the whole fact.
describe("a room's visibility is a tag with no value", () => {
	function metadataTagged(tags: string[][]): BuzzEvent {
		return { id: "e1", pubkey: "p1", created_at: 0, kind: 39000, tags, content: "", sig: "s" };
	}

	test("a private room carries the tag", () => {
		expect(carriesTag(metadataTagged([["d", "c1"], ["name", "HR"], ["private"]]), "private")).toBe(true);
	});

	test("a public room does not", () => {
		expect(carriesTag(metadataTagged([["d", "c1"], ["name", "광장"], ["public"]]), "private")).toBe(false);
	});
});
