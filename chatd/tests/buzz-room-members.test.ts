import { describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL_UUID = "8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f";
const AGENT_HEX = "a".repeat(64);
const CHIEF_HEX = "b".repeat(64);
const DIRECTOR_HEX = "c".repeat(64);
const GUEST_HEX = "d".repeat(64);

type RelayInjectable = {
	relay: {
		pubkeyHex: string;
		query: (filter: { kinds?: number[] }) => Promise<BuzzEvent[]>;
	};
};

function rosterEvent(createdAt: number, pubkeys: string[]): BuzzEvent {
	return {
		id: "e".repeat(64),
		pubkey: "f".repeat(64),
		created_at: createdAt,
		kind: 39002,
		tags: [["d", CHANNEL_UUID], ...pubkeys.map((pubkey) => ["p", pubkey])],
		content: "",
		sig: "0".repeat(128),
	};
}

function adapterInRoom(rosters: BuzzEvent[]): BuzzAdapter {
	const linksPath = join(mkdtempSync(join(tmpdir(), "chatd-room-")), "links.json");
	writeFileSync(
		linksPath,
		JSON.stringify({ [CHIEF_HEX]: "Chief@example.com", [DIRECTOR_HEX]: "director@example.com", [AGENT_HEX]: "agent@example.com" }),
	);
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
		accountLinksPath: linksPath,
	});
	(adapter as unknown as RelayInjectable).relay = {
		pubkeyHex: AGENT_HEX,
		query: async (filter) => (filter.kinds?.includes(39002) ? rosters : []),
	};
	return adapter;
}

describe("buzz room members", () => {
	test("names everyone in the room but the agent by the address they sign in with", async () => {
		const adapter = adapterInRoom([
			rosterEvent(1, [CHIEF_HEX]),
			rosterEvent(2, [CHIEF_HEX, DIRECTOR_HEX, AGENT_HEX]),
		]);
		const emails = await adapter.roomMemberEmailsOf(adapter.encodeThreadId({ channelId: CHANNEL_UUID }));
		expect(emails?.sort()).toEqual(["chief@example.com", "director@example.com"]);
	});

	test("names nobody when somebody in the room has no linked account", async () => {
		const adapter = adapterInRoom([rosterEvent(1, [CHIEF_HEX, DIRECTOR_HEX, GUEST_HEX])]);
		expect(await adapter.roomMemberEmailsOf(adapter.encodeThreadId({ channelId: CHANNEL_UUID }))).toBeUndefined();
	});
});
