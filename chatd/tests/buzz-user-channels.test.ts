import { describe, expect, test } from "bun:test";
import { createChannelTags, isLastOwner, openChannelsToJoin } from "../src/adapters/buzz/user-channels.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import { MalformedRequest, parseMemberExternalIDs, parseNewChannel } from "../src/personal/parse.ts";

function metadata(channelID: string, tags: string[][], createdAt = 1): BuzzEvent {
	return {
		id: `${channelID}-${createdAt}`,
		pubkey: "relay",
		created_at: createdAt,
		kind: 39000,
		tags: [["d", channelID], ...tags],
		content: "",
		sig: "",
	};
}

describe("createChannelTags", () => {
	test("names the channel the way handle_create_group reads it", () => {
		expect(
			createChannelTags("channel-1", {
				name: "# release-notes ",
				description: " What ships ",
				visibility: "private",
				memberPubkeyHexes: [],
			}),
		).toEqual([
			["h", "channel-1"],
			["name", "release-notes"],
			["visibility", "private"],
			["channel_type", "stream"],
			["about", "What ships"],
		]);
	});

	test("leaves the description out when there is none", () => {
		const tags = createChannelTags("channel-1", { name: "general", visibility: "open", memberPubkeyHexes: [] });
		expect(tags.some((tag) => tag[0] === "about")).toBe(false);
	});
});

describe("openChannelsToJoin", () => {
	test("offers only public channels the person has not joined", () => {
		const events = [
			metadata("open-1", [["name", "잡담"], ["public"], ["t", "stream"]]),
			metadata("joined", [["name", "광장"], ["public"]]),
			metadata("secret", [["name", "Admin"], ["private"]]),
			metadata("direct", [["name", ""], ["hidden"], ["t", "dm"]]),
			metadata("archived", [["name", "old"], ["public"], ["archived", "true"]]),
		];
		expect(openChannelsToJoin(events, new Set(["joined"]))).toEqual([
			{ channelID: "open-1", name: "잡담", description: undefined },
		]);
	});

	test("reads the latest metadata when a channel was edited", () => {
		const events = [
			metadata("open-1", [["name", "after"], ["private"]], 2),
			metadata("open-1", [["name", "before"], ["public"]], 1),
		];
		expect(openChannelsToJoin(events, new Set())).toEqual([]);
	});
});

describe("parseNewChannel", () => {
	const actor = { kind: "buzz-secret", secret: "s" };

	test("reads the channel a person asked for", () => {
		expect(
			parseNewChannel({ actor, name: "qa", visibility: "open", memberExternalIDs: ["a", "b"] }),
		).toEqual({ name: "qa", description: undefined, visibility: "open", memberExternalIDs: ["a", "b"] });
	});

	test("refuses a visibility the relay does not know", () => {
		expect(() => parseNewChannel({ actor, name: "qa", visibility: "public" })).toThrow(MalformedRequest);
	});

	test("refuses a name that is only a prefix", () => {
		expect(() => parseNewChannel({ actor, name: " # ", visibility: "open" })).toThrow(MalformedRequest);
	});

	test("refuses a channel without a name", () => {
		expect(() => parseNewChannel({ actor, visibility: "open" })).toThrow("missing required field name");
	});
});

describe("parseMemberExternalIDs", () => {
	test("reads the members a person asked to add", () => {
		expect(parseMemberExternalIDs({ memberExternalIDs: ["a", "b"] })).toEqual(["a", "b"]);
	});

	test("treats a missing list as nobody", () => {
		expect(parseMemberExternalIDs({})).toEqual([]);
	});

	test("refuses a list that holds something other than ids", () => {
		expect(() => parseMemberExternalIDs({ memberExternalIDs: ["a", 1] })).toThrow(MalformedRequest);
	});
});

describe("isLastOwner", () => {
	function roster(tags: string[][]): BuzzEvent {
		return { id: "r", pubkey: "relay", created_at: 1, kind: 39002, tags: [["d", "c"], ...tags], content: "", sig: "" };
	}

	test("is the person when nobody else owns the channel", () => {
		expect(isLastOwner(roster([["p", "me", "", "owner"], ["p", "you", "", "member"]]), "me")).toBe(true);
	});

	test("is not the person when another owner remains", () => {
		expect(isLastOwner(roster([["p", "me", "", "owner"], ["p", "you", "", "owner"]]), "me")).toBe(false);
	});

	test("is not a member who owns nothing", () => {
		expect(isLastOwner(roster([["p", "me", "", "member"], ["p", "you", "", "owner"]]), "me")).toBe(false);
	});

	test("is nobody when the roster is unknown", () => {
		expect(isLastOwner(undefined, "me")).toBe(false);
	});
});
