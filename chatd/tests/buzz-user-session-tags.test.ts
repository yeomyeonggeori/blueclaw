import { describe, expect, test } from "bun:test";
import { channelMessageTags } from "../src/adapters/buzz/user-session.ts";

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
