import { describe, expect, test } from "bun:test";
import { findDirectMessageChannelID } from "../src/adapters/buzz/user-session";

const GROUP_METADATA_KIND = 39000;
const GROUP_MEMBERS_KIND = 39002;

type StoredEvent = {
	id: string;
	pubkey: string;
	created_at: number;
	kind: number;
	tags: string[][];
	content: string;
	sig: string;
};

function event(kind: number, tags: string[][], created_at = 1): StoredEvent {
	return { id: `${kind}-${tags[0]?.[1] ?? ""}-${created_at}`, pubkey: "", created_at, kind, tags, content: "", sig: "" };
}

// The relay answers a filter; this stands in for it, matching on the fields the
// matcher actually asks by.
function aRelayHolding(events: StoredEvent[]) {
	return {
		query: async (filter: object) => {
			const asked = filter as { kinds?: number[]; "#p"?: string[]; "#d"?: string[] };
			return events.filter((held) => {
				if (asked.kinds && !asked.kinds.includes(held.kind)) return false;
				if (asked["#p"] && !held.tags.some((tag) => tag[0] === "p" && asked["#p"]?.includes(tag[1] ?? ""))) {
					return false;
				}
				if (asked["#d"] && !held.tags.some((tag) => tag[0] === "d" && asked["#d"]?.includes(tag[1] ?? ""))) {
					return false;
				}
				return true;
			});
		},
	};
}

describe("finding the direct room that already exists", () => {
	test("finds a room the counterpart never joined, from the roster alone", async () => {
		const relay = aRelayHolding([
			event(GROUP_METADATA_KIND, [["d", "room-1"], ["t", "dm"]]),
			event(GROUP_MEMBERS_KIND, [["d", "room-1"], ["p", "me"], ["p", "them"]]),
		]);
		expect(await findDirectMessageChannelID(relay, "me", "them")).toBe("room-1");
	});

	test("finds a room whose metadata names both", async () => {
		const relay = aRelayHolding([
			event(GROUP_METADATA_KIND, [["d", "room-2"], ["t", "dm"], ["p", "me"], ["p", "them"]]),
			event(GROUP_MEMBERS_KIND, [["d", "room-2"], ["p", "me"]]),
		]);
		expect(await findDirectMessageChannelID(relay, "me", "them")).toBe("room-2");
	});

	test("reads the newest roster when a room has been changed", async () => {
		const relay = aRelayHolding([
			event(GROUP_METADATA_KIND, [["d", "room-3"], ["t", "dm"]]),
			event(GROUP_MEMBERS_KIND, [["d", "room-3"], ["p", "me"], ["p", "somebody-else"]], 1),
			event(GROUP_MEMBERS_KIND, [["d", "room-3"], ["p", "me"], ["p", "them"]], 2),
		]);
		expect(await findDirectMessageChannelID(relay, "me", "them")).toBe("room-3");
	});

	test("does not take a group room, nor a room between other people", async () => {
		const relay = aRelayHolding([
			event(GROUP_METADATA_KIND, [["d", "room-4"], ["t", "stream"]]),
			event(GROUP_MEMBERS_KIND, [["d", "room-4"], ["p", "me"], ["p", "them"]]),
			event(GROUP_METADATA_KIND, [["d", "room-5"], ["t", "dm"]]),
			event(GROUP_MEMBERS_KIND, [["d", "room-5"], ["p", "them"], ["p", "somebody-else"]]),
		]);
		expect(await findDirectMessageChannelID(relay, "me", "them")).toBeUndefined();
	});

	test("says nothing when the person is in no room at all", async () => {
		expect(await findDirectMessageChannelID(aRelayHolding([]), "me", "them")).toBeUndefined();
	});
});
