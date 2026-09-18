import { beforeEach, describe, expect, mock, test } from "bun:test";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { UserChannelRole } from "../src/adapters/buzz/user-channels.ts";
import { MalformedRequest, parseMemberExternalIDs, parseNewChannel } from "../src/personal/parse.ts";

const CHANNEL = "channel-1";
const ACTOR_PUBKEY = "a".repeat(64);
const OTHER_MEMBER = "b".repeat(64);
const PUT_USER_KIND = 9000;
const REMOVE_USER_KIND = 9001;

type Published = { kind: number; content: string; tags: string[][] };

let members = new Map<string, UserChannelRole>();
let published: Published[] = [];

function rosterEvent(): BuzzEvent {
	return {
		id: "roster",
		pubkey: "relay",
		created_at: 1,
		kind: 39002,
		tags: [["d", CHANNEL], ...[...members].map(([pubkey, role]) => ["p", pubkey, "", role])],
		content: "",
		sig: "",
	};
}

function applyToRoster(kind: number, tags: string[][]): void {
	const pubkey = tags.find((tag) => tag[0] === "p")?.[1];
	if (!pubkey) return;
	if (kind === PUT_USER_KIND) {
		const role = tags.find((tag) => tag[0] === "role")?.[1];
		members.set(pubkey, role === "owner" || role === "admin" ? role : "member");
	} else if (kind === REMOVE_USER_KIND) {
		members.delete(pubkey);
	}
}

const relay = {
	pubkeyHex: ACTOR_PUBKEY,
	connect: async () => {},
	disconnect: () => {},
	subscribe: () => {},
	query: async () => [rosterEvent()],
	publish: async (kind: number, content: string, tags: string[][]) => {
		published.push({ kind, content, tags });
		applyToRoster(kind, tags);
		return { id: "published", pubkey: ACTOR_PUBKEY, created_at: 300, kind, tags, content, sig: "" };
	},
	publishForAcknowledgement: async () => "",
};

mock.module("../src/adapters/buzz/relay-client.ts", () => ({ createBuzzRelayClient: () => relay }));

const {
	addChannelOwnerAsUser,
	createChannelTags,
	isLastOwner,
	openChannelsToJoin,
	removeChannelMemberAsUser,
	rolesOnRoster,
	TargetIsChannelOwner,
} = await import("../src/adapters/buzz/user-channels.ts");

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

describe("rolesOnRoster", () => {
	function roster(tags: string[][]): BuzzEvent {
		return { id: "r", pubkey: "relay", created_at: 1, kind: 39002, tags: [["d", "c"], ...tags], content: "", sig: "" };
	}

	test("reads the role the relay wrote beside each member", () => {
		expect([...rolesOnRoster(roster([["p", "me", "", "owner"], ["p", "you", "", "admin"]]))]).toEqual([
			["me", "owner"],
			["you", "admin"],
		]);
	});

	test("calls anything else a member", () => {
		expect(rolesOnRoster(roster([["p", "me", "", "guest"], ["p", "you"]]))).toEqual(
			new Map([
				["me", "member"],
				["you", "member"],
			]),
		);
	});

	test("knows nobody from an unknown roster", () => {
		expect(rolesOnRoster(undefined).size).toBe(0);
	});
});

describe("addChannelOwnerAsUser", () => {
	beforeEach(() => {
		published = [];
		members = new Map([[ACTOR_PUBKEY, "owner"], [OTHER_MEMBER, "member"]]);
	});

	test("leaves the actor an owner and produces a roster with two owners", async () => {
		await addChannelOwnerAsUser({
			relayURL: "wss://relay",
			userSecretHex: "irrelevant-secret",
			channelID: CHANNEL,
			newOwnerPubkeyHex: OTHER_MEMBER,
		});

		expect(published).toEqual([
			{ kind: PUT_USER_KIND, content: "", tags: [["h", CHANNEL], ["p", OTHER_MEMBER], ["role", "owner"]] },
		]);
		const roles = rolesOnRoster(rosterEvent());
		expect(roles.get(ACTOR_PUBKEY)).toBe("owner");
		expect(roles.get(OTHER_MEMBER)).toBe("owner");
		expect([...roles.values()].filter((role) => role === "owner")).toHaveLength(2);
	});
});

describe("removeChannelMemberAsUser", () => {
	beforeEach(() => {
		published = [];
		members = new Map([[ACTOR_PUBKEY, "owner"], [OTHER_MEMBER, "member"]]);
	});

	test("publishes the removal of a plain member", async () => {
		await removeChannelMemberAsUser({
			relayURL: "wss://relay",
			userSecretHex: "irrelevant-secret",
			channelID: CHANNEL,
			memberPubkeyHex: OTHER_MEMBER,
		});

		expect(published).toEqual([
			{ kind: REMOVE_USER_KIND, content: "", tags: [["h", CHANNEL], ["p", OTHER_MEMBER]] },
		]);
		expect(rolesOnRoster(rosterEvent()).has(OTHER_MEMBER)).toBe(false);
	});

	test("refuses to remove a member whose roster role is owner", async () => {
		members.set(OTHER_MEMBER, "owner");

		await expect(
			removeChannelMemberAsUser({
				relayURL: "wss://relay",
				userSecretHex: "irrelevant-secret",
				channelID: CHANNEL,
				memberPubkeyHex: OTHER_MEMBER,
			}),
		).rejects.toThrow(TargetIsChannelOwner);
		expect(published).toEqual([]);
	});
});
