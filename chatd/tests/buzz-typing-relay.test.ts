import { afterAll, beforeAll, describe, expect, mock, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey, getPublicKey } from "nostr-tools/pure";
import { bytesToHex, hexToBytes } from "nostr-tools/utils";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import { createRelayConnection } from "../src/adapters/buzz/relay-connection.ts";
import { announceTypingAsUser } from "../src/adapters/buzz/user-typing.ts";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { BuzzArrivalWatch, type Typing } from "../src/personal/buzz-arrival-watch.ts";
import { normalizedInboundRoutingOf } from "../src/bridge.ts";
import { createOutboundHandler } from "../src/outbound.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

function connectionAs(url: string, privateKeyHex: string) {
	const secretKey = hexToBytes(privateKeyHex);
	return createRelayConnection(url, {
		pubkeyHex: getPublicKey(secretKey),
		signEvent: (kind: number, content: string, tags: string[][]) =>
			finalizeEvent({ kind, content, tags, created_at: Math.floor(Date.now() / 1000) }, secretKey) as BuzzEvent,
	});
}

const connectionsMade: ReturnType<typeof connectionAs>[] = [];

function recordedConnectionAs(url: string, privateKeyHex: string) {
	const connection = connectionAs(url, privateKeyHex);
	connectionsMade.push(connection);
	return connection;
}

mock.module("../src/adapters/buzz/relay-client.ts", () => ({ createBuzzRelayClient: recordedConnectionAs }));

type Filter = { kinds?: number[]; "#h"?: string[] };
type Subscription = { socket: ServerWebSocket<unknown>; subscriptionID: string; filters: Filter[] };

const subscriptions: Subscription[] = [];
const kept: BuzzEvent[] = [];
const published: BuzzEvent[] = [];

function wants(filter: Filter, event: BuzzEvent): boolean {
	if (filter.kinds && !filter.kinds.includes(event.kind)) return false;
	const channelID = event.tags.find((tag) => tag[0] === "h")?.[1] ?? "";
	return !filter["#h"] || filter["#h"].includes(channelID);
}

function isEphemeral(event: BuzzEvent): boolean {
	return event.kind >= 20_000 && event.kind < 30_000;
}

function answer(socket: ServerWebSocket<unknown>, raw: string): void {
	const [frameType, ...rest] = JSON.parse(raw) as [string, ...unknown[]];
	if (frameType === "AUTH") {
		socket.send(JSON.stringify(["OK", (rest[0] as BuzzEvent).id, true, ""]));
		return;
	}
	if (frameType === "EVENT") {
		const event = rest[0] as BuzzEvent;
		published.push(event);
		if (!isEphemeral(event)) kept.push(event);
		socket.send(JSON.stringify(["OK", event.id, true, ""]));
		for (const subscription of subscriptions) {
			if (!subscription.filters.some((filter) => wants(filter, event))) continue;
			subscription.socket.send(JSON.stringify(["EVENT", subscription.subscriptionID, event]));
		}
		return;
	}
	if (frameType !== "REQ") return;
	const [subscriptionID, ...filters] = rest as [string, ...Filter[]];
	subscriptions.push({ socket, subscriptionID, filters });
	socket.send(JSON.stringify(["EOSE", subscriptionID]));
}

let server: Server<unknown>;
let relayURL = "";

beforeAll(() => {
	server = Bun.serve({
		port: 0,
		fetch(request, serving) {
			if (serving.upgrade(request, { data: undefined })) return undefined;
			return new Response("a relay speaks websocket", { status: 400 });
		},
		websocket: {
			open(socket) {
				socket.send(JSON.stringify(["AUTH", "challenge"]));
			},
			message(socket, raw) {
				answer(socket, String(raw));
			},
		},
	});
	relayURL = `ws://127.0.0.1:${server.port}`;
});

afterAll(() => {
	for (const connection of connectionsMade) connection.disconnect();
	closeEveryPooledRelay();
	server.stop(true);
});

function watchTellingInto(typed: Typing[], participants: string[], channelID: string): BuzzArrivalWatch {
	return new BuzzArrivalWatch({
		openRelay: (secret) => connectionAs(relayURL, secret),
		listConversations: async () => [
			{ channelID, name: "", isDM: true, isPrivate: true, participantPubkeyHexes: participants },
		],
		tell: async () => {},
		tellTyping: async (_url, typing) => {
			typed.push(typing);
		},
		now: () => Date.now(),
	});
}

function isSubscribedTo(channelID: string): boolean {
	return subscriptions.some((subscription) => subscription.filters.some((filter) => filter["#h"]?.includes(channelID)));
}

function messageWrittenBy(secretKey: Uint8Array, channelID: string, threadTags: string[][] = []): BuzzEvent {
	return finalizeEvent(
		{
			kind: 9,
			content: "이번 주 일정 정리해줘",
			tags: [["h", channelID], ...threadTags],
			created_at: Math.floor(Date.now() / 1000),
		},
		secretKey,
	) as BuzzEvent;
}

function routingOf(adapter: BuzzAdapter, written: BuzzEvent) {
	return normalizedInboundRoutingOf(adapter, { id: adapter.parseMessage(written).threadId }, { id: written.id, raw: written });
}

function chatdServingBuzzAs(agentSecretHex: string) {
	const adapter = new BuzzAdapter({ relayURL, privateKeyHex: agentSecretHex, botDisplayName: "internkim" });
	const configuration: ChatdConfiguration = {
		botUserName: "internkim",
		blueclawBaseURL: "http://127.0.0.1:1",
		blueclawIngressURL: undefined,
		relayInboundURL: undefined,
		admindBaseURL: undefined,
		listenPort: 0,
		listenHostname: "127.0.0.1",
		mattermost: undefined,
		buzz: { relayURL, privateKeyHex: agentSecretHex, accountLinksPath: undefined, authTagJSON: undefined },
	};
	const handle = createOutboundHandler({ buzz: adapter }, configuration);
	const ask = async (capability: string, body: unknown): Promise<number> => {
		const response = await handle(
			new Request(`http://127.0.0.1/v1/platform/buzz/${capability}`, {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify(body),
			}),
		);
		return response.status;
	};
	return { adapter, ask };
}

async function until(condition: () => boolean): Promise<void> {
	const deadline = Date.now() + 2_000;
	while (!condition()) {
		if (Date.now() > deadline) throw new Error("the relay never delivered what was waited for");
		await new Promise((resolve) => setTimeout(resolve, 10));
	}
}

describe("typing against a relay", () => {
	test("one person's typing reaches the other's watcher over the wire, and the relay keeps nothing", async () => {
		const typist = generateSecretKey();
		const reader = generateSecretKey();
		const participants = [getPublicKey(typist), getPublicKey(reader)];
		const typed: Typing[] = [];
		const watch = new BuzzArrivalWatch({
			openRelay: (secret) => connectionAs(relayURL, secret),
			listConversations: async () => [
				{ channelID: "channel-1", name: "", isDM: true, isPrivate: true, participantPubkeyHexes: participants },
			],
			tell: async () => {},
			tellTyping: async (_url, typing) => {
				typed.push(typing);
			},
			now: () => Date.now(),
		});
		await watch.watch(bytesToHex(reader), "http://127.0.0.1:1/arrived", "http://127.0.0.1:1/typing");
		await until(() => subscriptions.some((subscription) => subscription.filters.some((filter) => filter["#h"])));

		await announceTypingAsUser({ relayURL, userSecretHex: bytesToHex(typist), channelID: "channel-1" });
		await until(() => typed.length > 0);

		expect(typed).toEqual([
			{ conversationID: "channel-1", authorExternalID: getPublicKey(typist), recipientExternalIDs: participants },
		]);
		expect(kept).toEqual([]);
		watch.closeEvery();
	});

	test("the agent answering a person's message is seen typing by that person, in the conversation they wrote in", async () => {
		const agent = generateSecretKey();
		const person = generateSecretKey();
		const participants = [getPublicKey(person), getPublicKey(agent)];
		const typed: Typing[] = [];
		const watch = watchTellingInto(typed, participants, "channel-2");
		await watch.watch(bytesToHex(person), "http://127.0.0.1:1/arrived", "http://127.0.0.1:1/typing");
		await until(() => isSubscribedTo("channel-2"));
		const chatd = chatdServingBuzzAs(bytesToHex(agent));
		await connectionsMade.at(-1)?.connect();

		const written = messageWrittenBy(person, "channel-2");
		const routing = routingOf(chatd.adapter, written);
		const status = await chatd.ask("progress.start", {
			replyTargetID: routing.replyTargetID,
			answeringMessageID: written.id,
		});
		expect(status).toBe(200);
		await until(() => typed.length > 0);

		expect(typed).toEqual([
			{ conversationID: "channel-2", authorExternalID: getPublicKey(agent), recipientExternalIDs: participants },
		]);
		watch.closeEvery();
	});

	test("the agent answering a reply inside a thread types in that thread", async () => {
		const agent = generateSecretKey();
		const chatd = chatdServingBuzzAs(bytesToHex(agent));
		await connectionsMade.at(-1)?.connect();
		const rootID = "a".repeat(64);
		const written = messageWrittenBy(generateSecretKey(), "channel-3", [["e", rootID, "", "root"]]);

		const status = await chatd.ask("progress.start", {
			replyTargetID: routingOf(chatd.adapter, written).replyTargetID,
			answeringMessageID: written.id,
		});
		expect(status).toBe(200);
		await until(() => published.some((event) => event.pubkey === getPublicKey(agent)));

		const typing = published.find((event) => event.pubkey === getPublicKey(agent));
		expect(typing?.kind).toBe(20002);
		expect(typing?.tags).toEqual([
			["h", "channel-3"],
			["e", rootID, "", "root"],
		]);
	});
});
