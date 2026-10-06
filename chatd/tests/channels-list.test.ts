import { expect, test } from "bun:test";

import { BuzzAdapter } from "../src/adapters/buzz/adapter";
import type { ChatdConfiguration } from "../src/configuration";
import { createOutboundHandler } from "../src/outbound";

const GROUP_METADATA_KIND = 39000;

function metadataEvent(channelID: string, name: string, createdAt: number, extraTags: string[][] = []) {
	return { id: `${channelID}-${createdAt}`, pubkey: "a".repeat(64), created_at: createdAt, kind: GROUP_METADATA_KIND, tags: [["d", channelID], ["name", name], ...extraTags], content: "", sig: "" };
}

function adapterWithChannels(events: object[]): BuzzAdapter {
	const adapter = new BuzzAdapter({ relayURL: "ws://localhost:3000", privateKeyHex: "1".repeat(64), botDisplayName: "internkim" });
	(adapter as unknown as { relay: { query: unknown; pubkeyHex: string } }).relay = {
		pubkeyHex: adapter.botPubkey,
		query: async () => events,
	};
	return adapter;
}

function listChannels(adapter: BuzzAdapter) {
	const handler = createOutboundHandler({ buzz: adapter }, {} as ChatdConfiguration);
	return handler(new Request("http://chatd/v1/platform/buzz/channels.list", { method: "POST", body: "{}" }));
}

test("channels.list answers each live channel by its id and its latest name", async () => {
	const adapter = adapterWithChannels([
		metadataEvent("channel-1", "old-name", 10),
		metadataEvent("channel-1", "#general", 20),
		metadataEvent("channel-2", "random", 5),
		metadataEvent("channel-3", "retired", 5, [["archived", "true"]]),
	]);

	const response = await listChannels(adapter);

	expect(response.status).toBe(200);
	expect(await response.json()).toEqual({
		channels: [
			{ channelID: "channel-1", name: "general" },
			{ channelID: "channel-2", name: "random" },
		],
	});
});

test("channelIdByName still finds a channel through the same listing", async () => {
	const adapter = adapterWithChannels([metadataEvent("channel-2", "random", 5)]);

	expect(await adapter.channelIdByName("#random")).toBe("channel-2");
	expect(await adapter.channelIdByName("missing")).toBeUndefined();
});
