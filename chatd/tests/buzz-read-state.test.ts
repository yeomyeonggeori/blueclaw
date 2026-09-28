import { describe, expect, test } from "bun:test";
import {
	READ_STATE_CLIENT_ID,
	READ_STATE_HORIZON_SECONDS,
	READ_STATE_KIND,
	countUnread,
	isReadStateEvent,
	mergeReadAt,
	parseReadStateBlob,
	readStateDTag,
	readStateSlotID,
	serializeReadStateBlob,
	unreadSince,
	withContextReadAt,
} from "../src/adapters/buzz/read-state.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const reader = "a".repeat(64);
const writer = "b".repeat(64);
const now = 1_800_000_000;

function message(pubkey: string, createdAt: number, tags: string[][] = [["h", "channel-1"]]): BuzzEvent {
	return { id: `message-${createdAt}`, pubkey, created_at: createdAt, kind: 9, tags, content: "", sig: "" };
}

function readStateEvent(tags: string[][]): BuzzEvent {
	return { id: "read-state-1", pubkey: reader, created_at: now, kind: READ_STATE_KIND, tags, content: "", sig: "" };
}

describe("read-state slot", () => {
	test("is the same 32 lowercase hex characters for a person every time", () => {
		const slotID = readStateSlotID(reader);
		expect(slotID).toMatch(/^[0-9a-f]{32}$/);
		expect(readStateSlotID(reader)).toBe(slotID);
		expect(readStateSlotID(writer)).not.toBe(slotID);
	});

	test("an event is read state only with one well-formed d tag and one read-state topic", () => {
		const d = ["d", readStateDTag(readStateSlotID(reader))];
		const topic = ["t", "read-state"];
		expect(isReadStateEvent(readStateEvent([d, topic]))).toBe(true);
		expect(isReadStateEvent(readStateEvent([topic]))).toBe(false);
		expect(isReadStateEvent(readStateEvent([d, d, topic]))).toBe(false);
		expect(isReadStateEvent(readStateEvent([d]))).toBe(false);
		expect(isReadStateEvent(readStateEvent([["d", "read-state:NOT-HEX"], topic]))).toBe(false);
		expect(isReadStateEvent(readStateEvent([["d", "settings"], topic]))).toBe(false);
	});
});

describe("read-state blob", () => {
	test("reads back what was written, escaping a context that looks reserved", () => {
		const blob = { clientID: READ_STATE_CLIENT_ID, readAtOfContext: new Map([["channel-1", now], ["ov_s:odd", 5]]) };
		const wire = serializeReadStateBlob(blob);
		expect(JSON.parse(wire).contexts).toEqual({ "channel-1": now, "esc:ov_s:odd": 5 });
		expect(parseReadStateBlob(wire)).toEqual(blob);
	});

	test("refuses a blob another schema version or no client wrote", () => {
		expect(parseReadStateBlob(JSON.stringify({ v: 2, client_id: "x", contexts: {} }))).toBeNull();
		expect(parseReadStateBlob(JSON.stringify({ v: 1, contexts: {} }))).toBeNull();
		expect(parseReadStateBlob(JSON.stringify({ v: 1, client_id: "x" }))).toBeNull();
		expect(parseReadStateBlob("not json")).toBeNull();
	});

	test("drops a bad entry and keeps the rest, and leaves manual-unread overrides alone", () => {
		const parsed = parseReadStateBlob(
			JSON.stringify({ v: 1, client_id: "x", contexts: { good: 10, fraction: 1.5, negative: -1, "ov_c:good": 3 } }),
		);
		expect(parsed?.readAtOfContext).toEqual(new Map([["good", 10]]));
	});

	test("a conversation counts as read up to the latest time any device read it", () => {
		const phone = { clientID: "phone", readAtOfContext: new Map([["channel-1", 100], ["channel-2", 50]]) };
		const web = { clientID: READ_STATE_CLIENT_ID, readAtOfContext: new Map([["channel-1", 80]]) };
		expect(mergeReadAt([phone, web])).toEqual(new Map([["channel-1", 100], ["channel-2", 50]]));
	});

	test("marking read never moves a conversation backwards and forgets what fell past the horizon", () => {
		const old = now - READ_STATE_HORIZON_SECONDS - 1;
		const own = { clientID: READ_STATE_CLIENT_ID, readAtOfContext: new Map([["channel-1", now], ["stale", old]]) };
		const updated = withContextReadAt(own, "channel-1", now - 10, now);
		expect(updated.readAtOfContext).toEqual(new Map([["channel-1", now]]));
		expect(withContextReadAt(null, "channel-2", now, now).readAtOfContext).toEqual(new Map([["channel-2", now]]));
	});
});

describe("unread count", () => {
	test("counts what others wrote in the conversation after the read time", () => {
		const messages = [message(writer, 90), message(writer, 110), message(writer, 120)];
		expect(countUnread(messages, reader, 100)).toBe(2);
	});

	test("what the reader wrote is never unread to them", () => {
		expect(countUnread([message(reader, 110), message(writer, 120)], reader, 100)).toBe(1);
	});

	test("a reply inside a thread does not count toward the conversation", () => {
		const reply = message(writer, 110, [["h", "channel-1"], ["e", "root-1", "", "root"]]);
		expect(countUnread([reply, message(writer, 120)], reader, 100)).toBe(1);
	});

	test("a conversation never read counts from the horizon, not from the beginning", () => {
		expect(unreadSince(undefined, now)).toBe(now - READ_STATE_HORIZON_SECONDS);
		expect(unreadSince(now - 5, now)).toBe(now - 5);
		expect(unreadSince(1, now)).toBe(now - READ_STATE_HORIZON_SECONDS);
	});
});
