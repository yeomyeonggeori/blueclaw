import { describe, expect, test } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { MattermostAdapter } from "../src/adapters/mattermost/adapter.ts";
import { EDIT_MESSAGE_KIND, STREAM_MESSAGE_KIND, type BuzzEvent } from "../src/adapters/buzz/types.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import { createOutboundHandler } from "../src/outbound.ts";
import { MalformedRequest, parsePersonRequest } from "../src/personal/parse.ts";

const CHANNEL = "8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f";
const QUESTION_ID = "a".repeat(64);
const PERSON = "c".repeat(64);

type Published = { kind: number; content: string; tags: string[][] };
type Filter = { ids?: string[]; kinds?: number[]; "#e"?: string[] };

function buzzAdapterOverFakeRelay(events: BuzzEvent[] = []): { adapter: BuzzAdapter; published: Published[] } {
	const adapter = new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: "1".repeat(64),
		botDisplayName: "internkim",
	});
	const published: Published[] = [];
	const relay = {
		pubkeyHex: adapter.botPubkey,
		publish: async (kind: number, content: string, tags: string[][]) => {
			published.push({ kind, content, tags });
			return { id: "posted-1", kind, content, tags, pubkey: adapter.botPubkey, created_at: 0, sig: "" };
		},
		query: async (filter: Filter) =>
			events.filter((event) => {
				if (filter.ids && !filter.ids.includes(event.id)) return false;
				if (filter.kinds && !filter.kinds.includes(event.kind)) return false;
				const targets = event.tags.filter((tag) => tag[0] === "e").map((tag) => tag[1]);
				return !filter["#e"] || filter["#e"].some((target) => targets.includes(target));
			}),
	};
	Object.assign(adapter, { relay });
	return { adapter, published };
}

function configuration(): ChatdConfiguration {
	return {
		botUserName: "internkim",
		blueclawBaseURL: "http://blueclaw.test",
		blueclawIngressURL: undefined,
		relayInboundURL: undefined,
		admindBaseURL: undefined,
		listenPort: 18090,
		listenHostname: "127.0.0.1",
		mattermost: undefined,
		buzz: undefined,
	};
}

async function callBuzz(adapter: BuzzAdapter, capabilityName: string, body: unknown): Promise<Response> {
	const handler = createOutboundHandler({ buzz: adapter }, configuration());
	return handler(
		new Request(`https://chatd.internal/v1/platform/buzz/${capabilityName}`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(body),
		}),
	);
}

function event(overrides: Partial<BuzzEvent>): BuzzEvent {
	return {
		id: QUESTION_ID,
		pubkey: PERSON,
		created_at: 1784900000,
		kind: STREAM_MESSAGE_KIND,
		tags: [["h", CHANNEL]],
		content: "",
		sig: "",
		...overrides,
	};
}

describe("a question the agent asks on Buzz", () => {
	test("carries its choices as tags, the recommended one marked", async () => {
		const { adapter, published } = buzzAdapterOverFakeRelay();

		await callBuzz(adapter, "reply.send", {
			replyTargetID: adapter.encodeThreadId({ channelId: CHANNEL }),
			message: "어느 쪽으로 할까요?",
			interaction: {
				interactionID: "interaction-1",
				kind: "approval",
				targetPlatformUserID: PERSON,
				recommendedOptionKey: "approve",
				options: [
					{ key: "approve", label: "승인" },
					{ key: "deny", label: "거절" },
				],
			},
		});

		expect(published[0]?.content).toBe("어느 쪽으로 할까요?\n\n1. 승인\n2. 거절");
		expect(published[0]?.tags).toContainEqual(["ask", "interaction-1", "approval", PERSON]);
		expect(published[0]?.tags.filter((tag) => tag[0] === "option")).toEqual([
			["option", "approve", "승인", "recommended"],
			["option", "deny", "거절"],
		]);
	});

	test("asked of anyone is a choice with no target", async () => {
		const { adapter, published } = buzzAdapterOverFakeRelay();

		await callBuzz(adapter, "reply.send", {
			replyTargetID: adapter.encodeThreadId({ channelId: CHANNEL }),
			message: "점심 메뉴",
			interaction: { interactionID: "interaction-2", options: [{ key: "a", label: "국밥" }] },
		});

		expect(published[0]?.tags).toContainEqual(["ask", "interaction-2", "choice", ""]);
	});

	test("an approval with unlabelled options keeps its written words and still tags every option", async () => {
		const { adapter, published } = buzzAdapterOverFakeRelay();

		await callBuzz(adapter, "reply.send", {
			replyTargetID: adapter.encodeThreadId({ channelId: CHANNEL }),
			message: "이 메일을 보낼까요?",
			interaction: {
				interactionID: "interaction-4",
				kind: "approval",
				question: "보낼까요?",
				options: [
					{ key: "approve", label: "" },
					{ key: "reject", label: "" },
				],
			},
		});

		expect(published[0]?.content).toBe("이 메일을 보낼까요?");
		expect(published[0]?.tags.filter((tag) => tag[0] === "option")).toEqual([
			["option", "approve", ""],
			["option", "reject", ""],
		]);
	});

	test("without choices carries no ask tags", async () => {
		const { adapter, published } = buzzAdapterOverFakeRelay();

		await callBuzz(adapter, "reply.send", {
			replyTargetID: adapter.encodeThreadId({ channelId: CHANNEL }),
			message: "다 됐어요",
			interaction: { interactionID: "interaction-3", options: [] },
		});

		expect(published[0]?.tags.some((tag) => tag[0] === "ask" || tag[0] === "option")).toBe(false);
	});
});

describe("interaction.resolve", () => {
	test("records the chosen option on the question with its current words unchanged", async () => {
		const question = event({ content: "처음 쓴 말" });
		const laterEdit = event({
			id: "e".repeat(64),
			kind: EDIT_MESSAGE_KIND,
			created_at: 1784900100,
			tags: [["h", CHANNEL], ["e", QUESTION_ID]],
			content: "고친 말",
		});
		const { adapter, published } = buzzAdapterOverFakeRelay([question, laterEdit]);

		const response = await callBuzz(adapter, "interaction.resolve", {
			replyTargetID: adapter.encodeThreadId({ channelId: CHANNEL }),
			messageID: QUESTION_ID,
			interactionID: "interaction-1",
			choiceKey: "approve",
		});

		expect(response.status).toBe(200);
		expect(await response.json()).toEqual({});
		expect(published).toEqual([
			{
				kind: EDIT_MESSAGE_KIND,
				content: "고친 말",
				tags: [["h", CHANNEL], ["e", QUESTION_ID], ["resolved", "interaction-1", "approve"]],
			},
		]);
	});

	test("records an answer in the person's own words as no option", async () => {
		const { adapter, published } = buzzAdapterOverFakeRelay([event({ content: "질문" })]);

		await callBuzz(adapter, "interaction.resolve", {
			replyTargetID: adapter.encodeThreadId({ channelId: CHANNEL }),
			messageID: QUESTION_ID,
			interactionID: "interaction-1",
			choiceKey: "",
		});

		expect(published[0]?.content).toBe("질문");
		expect(published[0]?.tags).toContainEqual(["resolved", "interaction-1"]);
	});

	test("does nothing on a messenger that keeps no tags", async () => {
		const adapter = new MattermostAdapter({
			baseUrl: "https://mattermost.example.com",
			botToken: "test-token",
			userName: "mattermost-bot",
		});
		const handler = createOutboundHandler({ mattermost: adapter }, configuration());

		const response = await handler(
			new Request("https://chatd.internal/v1/platform/mattermost/interaction.resolve", {
				method: "POST",
				body: JSON.stringify({ replyTargetID: "mattermost:channel-1", messageID: "post-1", interactionID: "interaction-1" }),
			}),
		);

		expect(response.status).toBe(200);
		expect(await response.json()).toEqual({});
	});
});

describe("a person's answer to a choice", () => {
	test("reaches the agent as the option it names", () => {
		const { adapter } = buzzAdapterOverFakeRelay();

		const answered = event({ tags: [["h", CHANNEL], ["answer", "interaction-1", "approve"]] });

		expect(adapter.answeredChoiceOf(answered)).toEqual({ interactionID: "interaction-1", key: "approve" });
		expect(adapter.answeredChoiceOf(event({}))).toBeUndefined();
	});

	test("is refused at the door unless it names both the question and the option", () => {
		const actor = { kind: "buzz-secret", secret: "s" };

		expect(parsePersonRequest({ actor, answer: { interactionID: "interaction-1", key: "approve" } }).answer).toEqual({
			interactionID: "interaction-1",
			key: "approve",
		});
		expect(parsePersonRequest({ actor }).answer).toBeUndefined();
		expect(() => parsePersonRequest({ actor, answer: { interactionID: "interaction-1" } })).toThrow(MalformedRequest);
		expect(() => parsePersonRequest({ actor, answer: { interactionID: "", key: "approve" } })).toThrow(MalformedRequest);
	});
});
