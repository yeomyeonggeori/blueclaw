import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import type { Server } from "bun";
import { generateSecretKey, getPublicKey, verifyEvent } from "nostr-tools/pure";
import { bytesToHex } from "nostr-tools/utils";
import { createHash } from "node:crypto";
import {
	ReadStateRefused,
	markReadThroughAsUser,
	messageReadStatusesAsUser,
	readSummariesAsUser,
} from "../src/adapters/buzz/personal-read.ts";

type Asked = { method: string; url: string; body: string; authorization: { id: string; pubkey: string; tags: string[][]; valid: boolean } };

const reader = generateSecretKey();
const readerSecretHex = bytesToHex(reader);
const readerPubkey = getPublicKey(reader);
const channelID = "11111111-1111-4111-8111-111111111111";
const rootID = "a".repeat(64);
const replyID = "b".repeat(64);

let server: Server<unknown>;
let relayURL = "";
let asked: Asked[] = [];
let answerOf: (request: Asked) => Response = () => Response.json({});

function decodedAuthorization(header: string | null): Asked["authorization"] {
	const event = JSON.parse(Buffer.from((header ?? "").replace(/^Nostr /, ""), "base64").toString("utf8"));
	return { id: event.id, pubkey: event.pubkey, tags: event.tags, valid: verifyEvent(event) && event.kind === 27235 };
}

function tagValue(tags: string[][], name: string): string | undefined {
	return tags.find((tag) => tag[0] === name)?.[1];
}

function sidebarChannel(id: string, unread: object) {
	return { channel_id: id, unread, attention: { status: "exact", value: 0 }, latest_message_id: null, threads: { items: [], complete: true } };
}

beforeAll(() => {
	server = Bun.serve({
		port: 0,
		async fetch(request) {
			const body = await request.text();
			const entry = { method: request.method, url: request.url, body, authorization: decodedAuthorization(request.headers.get("authorization")) };
			asked.push(entry);
			return answerOf(entry);
		},
	});
	relayURL = `ws://127.0.0.1:${server.port}`;
});

afterAll(() => server.stop(true));

beforeEach(() => {
	asked = [];
});

describe("the relay's read state, asked as the reader", () => {
	test("every request is signed by the reader for its exact address and method, never twice the same", async () => {
		answerOf = () => Response.json({ account: {}, channels: [], next_cursor: null });
		await readSummariesAsUser(relayURL, readerSecretHex);
		await readSummariesAsUser(relayURL, readerSecretHex);

		const [first, second] = asked;
		expect(first?.authorization.valid).toBe(true);
		expect(first?.authorization.pubkey).toBe(readerPubkey);
		expect(tagValue(first?.authorization.tags ?? [], "u")).toBe(first?.url);
		expect(tagValue(first?.authorization.tags ?? [], "method")).toBe("GET");
		expect(first?.authorization.id).not.toBe(second?.authorization.id);
	});

	test("a write is signed over the exact body it carries", async () => {
		answerOf = () => Response.json({ outcomes: [{ status: "applied" }], projection_status: "not_requested" });
		await markReadThroughAsUser(relayURL, readerSecretHex, { channelID, threadRootID: rootID }, replyID);

		const [write] = asked;
		expect(JSON.parse(write?.body ?? "")).toEqual({
			intents: [{ type: "mark_through", target: { channel_id: channelID, root_id: rootID }, message_id: replyID }],
		});
		expect(tagValue(write?.authorization.tags ?? [], "payload")).toBe(createHash("sha256").update(write?.body ?? "").digest("hex"));
	});

	test("the sidebar is read page after page and keeps the relay's three kinds of count", async () => {
		const secondChannelID = "22222222-2222-4222-8222-222222222222";
		answerOf = (request) =>
			new URL(request.url).searchParams.get("cursor")
				? Response.json({ account: {}, channels: [sidebarChannel(secondChannelID, { status: "unknown" })], next_cursor: null })
				: Response.json({ account: {}, channels: [sidebarChannel(channelID, { status: "at_least", value: 7 })], next_cursor: channelID });

		const summaries = await readSummariesAsUser(relayURL, readerSecretHex);

		expect(summaries.map((summary) => [summary.channelID, summary.unread])).toEqual([
			[channelID, { status: "at_least", value: 7 }],
			[secondChannelID, { status: "unknown" }],
		]);
	});

	test("a mark the relay could not settle is a failure, a mark at a message that is gone is not", async () => {
		answerOf = () => Response.json({ outcomes: [{ status: "blocked" }] });
		await markReadThroughAsUser(relayURL, readerSecretHex, { channelID }, replyID);

		answerOf = () => Response.json({ outcomes: [{ status: "unknown", retryable: true }] });
		await expect(markReadThroughAsUser(relayURL, readerSecretHex, { channelID }, replyID)).rejects.toBeInstanceOf(ReadStateRefused);
	});

	test("messages are asked about twenty conversations at a time", async () => {
		answerOf = (request) => {
			const targets: { message_ids: string[] }[] = JSON.parse(new URL(request.url).searchParams.get("targets") ?? "[]");
			const contexts = targets.map((target) => ({
				status: "available",
				messages: target.message_ids.map((messageID) => ({ message_id: messageID, status: "unread", reason: "mention" })),
			}));
			return Response.json({ account: {}, contexts });
		};
		const messages = Array.from({ length: 21 }, (_, index) => ({
			channelID,
			threadRootID: index.toString(16).padStart(64, "0"),
			messageID: (index + 100).toString(16).padStart(64, "0"),
		}));

		const statuses = await messageReadStatusesAsUser(relayURL, readerSecretHex, messages);

		expect(asked).toHaveLength(2);
		expect([...statuses.values()].every((status) => status === "unread")).toBe(true);
		expect(statuses.size).toBe(21);
	});

	test("a relay that does not serve read state says so as unavailable", async () => {
		answerOf = () => Response.json({ error: { code: "not_found" } }, { status: 404 });

		const refusal = await readSummariesAsUser(relayURL, readerSecretHex).catch((error: unknown) => error);

		expect(refusal).toBeInstanceOf(ReadStateRefused);
		expect(refusal instanceof ReadStateRefused && refusal.isUnavailable).toBe(true);
	});
});
