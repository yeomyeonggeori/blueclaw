import { describe, expect, test } from "bun:test";
import type { Message } from "chat";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { messagesSentSince } from "../src/adapters/buzz/catch-up.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";
import { deliveredMessagesInMemory, type DeliveredMessages } from "../src/delivered-messages.ts";

const CHANNEL_UUID = "8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f";
const SENDER_HEX = "c".repeat(64);
const AGENT_SECRET = "1".repeat(64);
const nowSeconds = Math.floor(Date.now() / 1000);

type Injectable = {
	relay: {
		pubkeyHex: string;
		query: (filter: object) => Promise<BuzzEvent[]>;
		queryComplete: (filter: object) => Promise<{ events: BuzzEvent[]; complete: boolean }>;
		publish: () => Promise<BuzzEvent>;
		subscribe: () => void;
	};
	channelsById: Map<string, unknown>;
	listeningSinceSeconds: number;
	chat: {
		processMessage: (
			adapter: BuzzAdapter,
			threadId: string,
			messageFactory: () => Promise<Message<BuzzEvent>>,
		) => Promise<void>;
	} | null;
	dispatchIncomingEvent: (event: BuzzEvent) => Promise<void>;
};

function messageSent(id: string, secondsAgo: number): BuzzEvent {
	return {
		id: id.repeat(64).slice(0, 64),
		pubkey: SENDER_HEX,
		created_at: nowSeconds - secondsAgo,
		kind: 9,
		tags: [["h", CHANNEL_UUID]],
		content: `message ${id}`,
		sig: "f".repeat(128),
	};
}

type Handling = { handedOver: string[]; failNext: Set<string> };

function adapterOver(onTheRelay: BuzzEvent[], deliveredMessages: DeliveredMessages, listeningSinceSeconds = nowSeconds) {
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: AGENT_SECRET,
		botDisplayName: "internkim",
		deliveredMessages,
	});
	const handling: Handling = { handedOver: [], failNext: new Set() };
	const injectable = adapter as unknown as Injectable;
	injectable.relay = {
		pubkeyHex: "b".repeat(64),
		query: async () => [],
		queryComplete: async () => ({ events: onTheRelay, complete: true }),
		publish: async () => messageSent("0", 0),
		subscribe: () => undefined,
	};
	injectable.channelsById.set(CHANNEL_UUID, { channelId: CHANNEL_UUID, name: "", isDM: true });
	injectable.listeningSinceSeconds = listeningSinceSeconds;
	injectable.chat = {
		processMessage: async (_adapter, _threadId, messageFactory) => {
			const message = await messageFactory();
			if (handling.failNext.delete(message.id)) throw new Error("the relay inbound refused it");
			handling.handedOver.push(message.id);
		},
	};
	return { adapter, injectable, handling };
}

function deliveredSoFar(): DeliveredMessages {
	const delivered = deliveredMessagesInMemory();
	return { ...delivered, startedEmpty: false };
}

describe("catching up on missed Buzz messages", () => {
	test("a message the live subscription never handed over is handed over by the catch-up", async () => {
		const missed = messageSent("1", 600);
		const { adapter, handling } = adapterOver([missed], deliveredSoFar());

		await adapter.catchUpOnMissedMessages();

		expect(handling.handedOver).toEqual([missed.id]);
	});

	test("a message already handed over is not handed over again", async () => {
		const answered = messageSent("2", 600);
		const delivered = deliveredSoFar();
		delivered.record(answered.id, answered.created_at);
		const { adapter, handling } = adapterOver([answered], delivered);

		await adapter.catchUpOnMissedMessages();

		expect(handling.handedOver).toEqual([]);
	});

	test("a message the relay sends again on reconnect is handed over once", async () => {
		const message = messageSent("3", 5);
		const { injectable, handling } = adapterOver([], deliveredSoFar());

		await injectable.dispatchIncomingEvent(message);
		await injectable.dispatchIncomingEvent(message);

		expect(handling.handedOver).toEqual([message.id]);
	});

	test("a hand-over that fails is tried again by the next catch-up", async () => {
		const message = messageSent("4", 600);
		const { adapter, handling } = adapterOver([message], deliveredSoFar());
		handling.failNext.add(message.id);

		await adapter.catchUpOnMissedMessages();
		expect(handling.handedOver).toEqual([]);

		await adapter.catchUpOnMissedMessages();
		expect(handling.handedOver).toEqual([message.id]);
	});

	test("with no record yet, what was already there is counted as handed over rather than answered again", async () => {
		const beforeListening = messageSent("5", 600);
		const afterListening = messageSent("6", 0);
		const { adapter, handling } = adapterOver([beforeListening, afterListening], deliveredMessagesInMemory(), nowSeconds);

		await adapter.catchUpOnMissedMessages();
		expect(handling.handedOver).toEqual([]);

		await adapter.catchUpOnMissedMessages();
		expect(handling.handedOver).toEqual([afterListening.id]);
	});

	test("the agent's own messages are never handed over", async () => {
		const own = { ...messageSent("7", 600), pubkey: "b".repeat(64) };
		const { adapter, handling } = adapterOver([own], deliveredSoFar());

		await adapter.catchUpOnMissedMessages();

		expect(handling.handedOver).toEqual([]);
	});
});

describe("messagesSentSince", () => {
	test("pages back through a relay that answers five hundred at a time", async () => {
		const all = Array.from({ length: 700 }, (_, at) => ({ ...messageSent(String(at % 10), 700 - at), id: `${at}`.padStart(64, "0") }));
		const asked: Array<Record<string, unknown>> = [];
		const relay = {
			queryComplete: async (filter: object) => {
				const { until } = filter as { until?: number };
				asked.push(filter as Record<string, unknown>);
				const inRange = all.filter((event) => until === undefined || event.created_at <= until);
				return { events: inRange.slice(-500), complete: true };
			},
		};

		const found = await messagesSentSince(relay, [CHANNEL_UUID], nowSeconds - 1000);

		expect(found).toHaveLength(700);
		expect(asked.length).toBeGreaterThan(1);
		expect(found.map((event) => event.created_at)).toEqual([...found.map((event) => event.created_at)].sort((a, b) => a - b));
	});

	test("a relay that stops answering part-way is an error, so nothing is counted as seen", async () => {
		const relay = { queryComplete: async () => ({ events: [messageSent("8", 5)], complete: false }) };

		await expect(messagesSentSince(relay, [CHANNEL_UUID], nowSeconds - 1000)).rejects.toThrow("did not finish");
	});
});
