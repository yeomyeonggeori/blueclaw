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

describe("agent thread tags", () => {
	// A direct conversation is already a conversation. Threading inside it drew
	// a thread hanging off a thread on the person's screen.
	it("posts flat in a direct conversation", async () => {
		const { adapter, relay } = buzzAdapterOverFakeRelay();
		adapterInternals<{ channelsById: Map<string, object> }>(adapter).channelsById.set("dm-1", {
			channelId: "dm-1",
			name: "",
			isDM: true,
		});

		await adapter.postMessage(adapter.encodeThreadId({ channelId: "dm-1", rootEventId: "req-1" }), "안내", [
			["e", "answered-1", "", "reply"],
		]);

		expect(eTags(relay.published[0]?.tags ?? [])).toEqual([]);
	});

	// Answering a reply answers what it replied to: the root is the answered
	// message's own root, so every answer lands flat under one conversation.
	it("collapses a channel answer to the thread root", async () => {
		const answered: BuzzEvent = {
			id: "answered-1",
			kind: 9,
			content: "ㅇ",
			tags: [["e", "req-1", "", "root"]],
			pubkey: "person",
			created_at: 0,
			sig: "",
		} as BuzzEvent;
		const { adapter, relay } = buzzAdapterOverFakeRelay({ "answered-1": answered });
		adapterInternals<{ channelsById: Map<string, object> }>(adapter).channelsById.set("channel-1", {
			channelId: "channel-1",
			name: "잡담",
			isDM: false,
		});

		await adapter.postMessage(adapter.encodeThreadId({ channelId: "channel-1" }), "안내", [
			["e", "answered-1", "", "reply"],
		]);

		expect(eTags(relay.published[0]?.tags ?? [])).toEqual([
			["e", "req-1", "", "root"],
			["e", "req-1", "", "reply"],
		]);
	});
});
