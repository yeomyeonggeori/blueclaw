import { getToken } from "nostr-tools/nip98";
import { finalizeEvent, type EventTemplate } from "nostr-tools/pure";
import { hexToBytes } from "nostr-tools/utils";
import { blossomBaseURL } from "./blossom.ts";
import { fetchFromRelay } from "./relay-trust.ts";

// The relay's private read-state API, docs/buzz-v1-read-state.md in the Buzz
// source: the relay keeps each reader's progress and answers what is unread.
const basePath = "/buzz/v1/me";
const mostChannelsPerPage = 20;
const mostContextsPerQuery = 20;
const mostMessagesPerQuery = 100;
const unavailableStatuses = new Set([404, 429, 503]);

export type ReadCount = { status: "exact" | "at_least"; value: number } | { status: "unknown" };

export type UnreadThread = {
	rootID: string;
	unread: ReadCount;
	latestReplyID: string;
};

export type ChannelReadSummary = {
	channelID: string;
	unread: ReadCount;
	attention: ReadCount;
	latestMessageID?: string;
	unreadThreads: UnreadThread[];
};

export type ReadContext = { channelID: string; threadRootID?: string };

export type MessageReadStatus = "read" | "unread" | "not_counted" | "unknown" | "unavailable";

export class ReadStateRefused extends Error {
	constructor(
		readonly status: number,
		readonly code: string,
	) {
		super(`the relay's read state answered ${status} ${code}`);
		this.name = "ReadStateRefused";
	}

	get isUnavailable(): boolean {
		return unavailableStatuses.has(this.status);
	}
}

export async function readSummariesAsUser(relayURL: string, userSecretHex: string): Promise<ChannelReadSummary[]> {
	const summaries: ChannelReadSummary[] = [];
	let cursor: string | undefined;
	do {
		const page = await askRelay(relayURL, userSecretHex, "GET", sidebarPath(cursor));
		const record = asRecord(page, "sidebar page");
		summaries.push(...asArray(record.channels, "sidebar channels").map(channelSummaryOf));
		cursor = optionalString(record.next_cursor);
	} while (cursor);
	return summaries;
}

export async function markReadThroughAsUser(
	relayURL: string,
	userSecretHex: string,
	context: ReadContext,
	messageID: string,
): Promise<void> {
	const intent = { type: "mark_through", target: targetOf(context), message_id: messageID };
	const answer = asRecord(await askRelay(relayURL, userSecretHex, "POST", `${basePath}/read-state`, { intents: [intent] }), "read-state write");
	const [outcome] = asArray(answer.outcomes, "read-state outcomes").map((entry) => asRecord(entry, "read-state outcome"));
	const status = outcome?.status;
	if (status === "applied" || status === "blocked") return;
	throw new ReadStateRefused(status === "unknown" ? 503 : 400, String(status));
}

export async function messageReadStatusesAsUser(
	relayURL: string,
	userSecretHex: string,
	messages: (ReadContext & { messageID: string })[],
): Promise<Map<string, MessageReadStatus>> {
	const statuses = new Map<string, MessageReadStatus>();
	for (const batch of contextBatchesOf(messages)) {
		const path = `${basePath}/read-state?targets=${encodeURIComponent(JSON.stringify(batch))}`;
		const page = asRecord(await askRelay(relayURL, userSecretHex, "GET", path), "read-state page");
		asArray(page.contexts, "read-state contexts").forEach((context) => recordStatuses(statuses, asRecord(context, "read-state context")));
	}
	return statuses;
}

function sidebarPath(cursor: string | undefined): string {
	const query = new URLSearchParams({ limit: String(mostChannelsPerPage) });
	if (cursor) query.set("cursor", cursor);
	return `${basePath}/sidebar?${query}`;
}

function targetOf(context: ReadContext): Record<string, string> {
	return context.threadRootID
		? { channel_id: context.channelID, root_id: context.threadRootID }
		: { channel_id: context.channelID };
}

type ContextTarget = { target: Record<string, string>; message_ids: string[] };

function contextBatchesOf(messages: (ReadContext & { messageID: string })[]): ContextTarget[][] {
	const byContext = new Map<string, ContextTarget>();
	for (const message of messages) {
		const key = `${message.channelID}:${message.threadRootID ?? ""}`;
		const existing = byContext.get(key) ?? { target: targetOf(message), message_ids: [] };
		existing.message_ids.push(message.messageID);
		byContext.set(key, existing);
	}
	const batches: ContextTarget[][] = [];
	let batch: ContextTarget[] = [];
	let messagesInBatch = 0;
	for (const context of byContext.values()) {
		const isFull = batch.length === mostContextsPerQuery || messagesInBatch + context.message_ids.length > mostMessagesPerQuery;
		if (isFull) {
			batches.push(batch);
			batch = [];
			messagesInBatch = 0;
		}
		batch.push(context);
		messagesInBatch += context.message_ids.length;
	}
	if (batch.length > 0) batches.push(batch);
	return batches;
}

function recordStatuses(statuses: Map<string, MessageReadStatus>, context: Record<string, unknown>): void {
	if (context.status !== "available") return;
	for (const entry of asArray(context.messages, "read-state messages")) {
		const message = asRecord(entry, "read-state message");
		statuses.set(String(message.message_id), messageStatusOf(message.status));
	}
}

function messageStatusOf(value: unknown): MessageReadStatus {
	if (value === "read" || value === "unread" || value === "not_counted" || value === "unavailable") return value;
	return "unknown";
}

function channelSummaryOf(value: unknown): ChannelReadSummary {
	const channel = asRecord(value, "sidebar channel");
	const threads = asRecord(channel.threads, "sidebar threads");
	return {
		channelID: String(channel.channel_id),
		unread: readCountOf(channel.unread),
		attention: readCountOf(channel.attention),
		latestMessageID: optionalString(channel.latest_message_id),
		unreadThreads: asArray(threads.items, "sidebar thread items").map(unreadThreadOf),
	};
}

function unreadThreadOf(value: unknown): UnreadThread {
	const thread = asRecord(value, "sidebar thread");
	return {
		rootID: String(thread.root_id),
		unread: readCountOf(thread.unread),
		latestReplyID: String(thread.latest_reply_id),
	};
}

function readCountOf(value: unknown): ReadCount {
	const { status, value: counted } = asRecord(value, "read count");
	if (typeof counted !== "number") return { status: "unknown" };
	if (status === "exact" || status === "at_least") return { status, value: counted };
	return { status: "unknown" };
}

async function askRelay(
	relayURL: string,
	userSecretHex: string,
	method: "GET" | "POST",
	pathAndQuery: string,
	body?: Record<string, unknown>,
): Promise<unknown> {
	const url = blossomBaseURL(relayURL) + pathAndQuery;
	const authorization = await getToken(url, method, (event) => signedWithNonce(event, userSecretHex), true, body);
	const headers: Record<string, string> = { Authorization: authorization };
	if (body) headers["Content-Type"] = "application/json";
	const response = await fetchFromRelay(url, { method, headers, ...(body && { body: JSON.stringify(body) }) });
	const answer: unknown = await response.json().catch(() => ({}));
	if (!response.ok) throw new ReadStateRefused(response.status, errorCodeOf(answer));
	return answer;
}

// The relay refuses a NIP-98 event id it has seen (replay protection), and two
// identical requests in one second would share one; buzz-cli adds the same tag.
function signedWithNonce(event: EventTemplate, userSecretHex: string) {
	const tags = [...event.tags, ["nonce", crypto.randomUUID()]];
	return finalizeEvent({ ...event, tags }, hexToBytes(userSecretHex));
}

function errorCodeOf(answer: unknown): string {
	const error = isRecord(answer) ? answer.error : undefined;
	const code = isRecord(error) ? error.code : undefined;
	return typeof code === "string" ? code : "unavailable";
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asRecord(value: unknown, what: string): Record<string, unknown> {
	if (!isRecord(value)) throw new ReadStateRefused(502, `malformed ${what}`);
	return value;
}

function asArray(value: unknown, what: string): unknown[] {
	if (!Array.isArray(value)) throw new ReadStateRefused(502, `malformed ${what}`);
	return value;
}

function optionalString(value: unknown): string | undefined {
	return typeof value === "string" && value !== "" ? value : undefined;
}
