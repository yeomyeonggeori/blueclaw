import { describe, expect, it } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const AGENT_SECRET = "1".repeat(64);

function adapterInternals<T>(adapter: BuzzAdapter): T {
	return adapter as unknown as T;
}

type FakeRelay = {
	published: Array<{ kind: number; content: string; tags: string[][] }>;
	publish: (kind: number, content: string, tags: string[][]) => Promise<BuzzEvent>;
	query: (filter: object) => Promise<BuzzEvent[]>;
};

function buzzAdapterOverFakeRelay(eventsById: Record<string, BuzzEvent> = {}): {
	adapter: BuzzAdapter;
	relay: FakeRelay;
} {
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: AGENT_SECRET,
		botDisplayName: "internkim",
	});
	const relay: FakeRelay = {
		published: [],
		publish: async (kind, content, tags) => {
			relay.published.push({ kind, content, tags });
			return { id: "posted-1", kind, content, tags, pubkey: "agent", created_at: 0, sig: "" } as BuzzEvent;
		},
		query: async (filter) => {
			const ids = (filter as { ids?: string[] }).ids ?? [];
			return ids.map((id) => eventsById[id]).filter((event): event is BuzzEvent => Boolean(event));
		},
	};
	adapterInternals<{ relay: FakeRelay }>(adapter).relay = relay;
	return { adapter, relay };
}

function eTags(tags: string[][]): string[][] {
	return tags.filter((tag) => tag[0] === "e");
}

function insideThread(id: string, root: string): BuzzEvent {
	return {
		id,
		kind: 9,
		content: "이어서",
		tags: [["e", root, "", "root"]],
		pubkey: "person",
		created_at: 0,
		sig: "",
	} as BuzzEvent;
}

function topLevel(id: string): BuzzEvent {
	return { id, kind: 9, content: "ㅇ", tags: [], pubkey: "person", created_at: 0, sig: "" } as BuzzEvent;
}

function withChannel(adapter: BuzzAdapter, channelId: string, isDM: boolean): void {
	adapterInternals<{ channelsById: Map<string, object> }>(adapter).channelsById.set(channelId, {
		channelId,
		name: isDM ? "" : "잡담",
		isDM,
	});
}

// The answer lives in the answered message's thread: a top-level question opens
// its own thread, a threaded message is answered flat at its thread's root.
describe("agent thread tags", () => {
	it("answers inside the thread the person spoke in", async () => {
		const { adapter, relay } = buzzAdapterOverFakeRelay({ "answered-1": insideThread("answered-1", "req-1") });
		withChannel(adapter, "dm-1", true);

		await adapter.postMessage(adapter.encodeThreadId({ channelId: "dm-1" }), "안내", [
			["e", "answered-1", "", "reply"],
		]);

		expect(eTags(relay.published[0]?.tags ?? [])).toEqual([
			["e", "req-1", "", "root"],
			["e", "req-1", "", "reply"],
		]);
	});

	// A question asked at the top level of a direct conversation opens its own
	// thread, so two requests never mix on the timeline.
	it("answers a top-level direct message in a thread of its own", async () => {
		const { adapter, relay } = buzzAdapterOverFakeRelay({ "answered-1": topLevel("answered-1") });
		withChannel(adapter, "dm-1", true);

		await adapter.postMessage(adapter.encodeThreadId({ channelId: "dm-1", rootEventId: "answered-1" }), "안내", [
			["e", "answered-1", "", "reply"],
		]);

		expect(eTags(relay.published[0]?.tags ?? [])).toEqual([
			["e", "answered-1", "", "root"],
			["e", "answered-1", "", "reply"],
		]);
	});

	it("answers a top-level channel message in its thread", async () => {
		const { adapter, relay } = buzzAdapterOverFakeRelay({ "answered-1": topLevel("answered-1") });
		withChannel(adapter, "channel-1", false);

		await adapter.postMessage(adapter.encodeThreadId({ channelId: "channel-1" }), "안내", [
			["e", "answered-1", "", "reply"],
		]);

		expect(eTags(relay.published[0]?.tags ?? [])).toEqual([
			["e", "answered-1", "", "root"],
			["e", "answered-1", "", "reply"],
		]);
	});

	it("collapses a channel answer to the thread root", async () => {
		const { adapter, relay } = buzzAdapterOverFakeRelay({ "answered-1": insideThread("answered-1", "req-1") });
		withChannel(adapter, "channel-1", false);

		await adapter.postMessage(adapter.encodeThreadId({ channelId: "channel-1" }), "안내", [
			["e", "answered-1", "", "reply"],
		]);

		expect(eTags(relay.published[0]?.tags ?? [])).toEqual([
			["e", "req-1", "", "root"],
			["e", "req-1", "", "reply"],
		]);
	});
});
