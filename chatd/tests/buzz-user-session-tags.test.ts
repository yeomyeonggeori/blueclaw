import { describe, expect, test } from "bun:test";
import { imetaTag } from "../src/adapters/buzz/blossom.ts";
import { attachmentsOfTags, channelMessageTags } from "../src/adapters/buzz/user-session.ts";

describe("channelMessageTags", () => {
	test("a reply names the root it answers", () => {
		expect(channelMessageTags("channel-1", [], undefined, "root-1")).toEqual([
			["h", "channel-1"],
			["e", "root-1", "", "root"],
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
			},
		]);
	});

	test("an unnamed file falls back to what the address ends in", () => {
		expect(attachmentsOfTags([imetaTag(blob)]).map((file) => file.filename)).toEqual(["9f2c.pdf"]);
	});

	test("a tag that names no address describes nothing", () => {
		expect(attachmentsOfTags([["imeta", "m image/png"]])).toEqual([]);
	});
});
