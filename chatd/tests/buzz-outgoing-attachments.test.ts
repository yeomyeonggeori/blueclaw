import { afterEach, describe, expect, test } from "bun:test";
import { buildMessageBody } from "../src/adapters/buzz/user-session.ts";
import { AttachmentRefused } from "../src/outgoing-attachment.ts";
import { createOutboundHandler } from "../src/outbound.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const relayURL = "http://relay.test";
const userSecretHex = "11".repeat(32);
const realFetch = globalThis.fetch;

function serveBlossom(answer: (body: Uint8Array) => Response): string[] {
	const uploaded: string[] = [];
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		uploaded.push(String(input));
		return answer(new Uint8Array(init?.body as ArrayBuffer));
	}) as typeof fetch;
	return uploaded;
}

function accepting(): (body: Uint8Array) => Response {
	return (body) =>
		Response.json({
			url: `${relayURL}/media/abc.pdf`,
			sha256: "abc",
			size: body.byteLength,
			type: "application/pdf",
		});
}

afterEach(() => {
	globalThis.fetch = realFetch;
});

describe("a file the store has already been given", () => {
	test("is named where it lives, without being uploaded again", async () => {
		const uploaded = serveBlossom(accepting());

		const built = await buildMessageBody({
			relayURL,
			userSecretHex,
			message: "the deck",
			attachments: [
				{
					filename: "2026 예산.pdf",
					contentType: "application/pdf",
					address: "https://company.supabase.co/storage/v1/object/asset/c/shared/attachment/9f2c.pdf",
					sizeBytes: 2048,
					digest: "9f2c",
				},
			],
		});

		expect(uploaded).toEqual([]);
		expect(built.body).toBe(
			"the deck\n[2026 예산.pdf](https://company.supabase.co/storage/v1/object/asset/c/shared/attachment/9f2c.pdf)",
		);
		expect(built.mediaTags).toEqual([
			[
				"imeta",
				"url https://company.supabase.co/storage/v1/object/asset/c/shared/attachment/9f2c.pdf",
				"m application/pdf",
				"x 9f2c",
				"size 2048",
				"filename 2026 예산.pdf",
			],
		]);
	});
});

describe("a file the store will not take", () => {
	test("comes back naming which one it was and why", async () => {
		serveBlossom(() => new Response("disallowed content type: text/html", { status: 415 }));

		const refusal = await buildMessageBody({
			relayURL,
			userSecretHex,
			message: "",
			attachments: [{ filename: "page.html", contentType: "text/html", contentBase64: "AAAA" }],
		}).catch((error: unknown) => error);

		expect(refusal).toBeInstanceOf(AttachmentRefused);
		expect((refusal as AttachmentRefused).refusals).toEqual([
			{ index: 0, filename: "page.html", status: 415, reason: "disallowed content type: text/html" },
		]);
	});

	test("does not take the files sent beside it down with it", async () => {
		let call = 0;
		serveBlossom((body) => {
			call += 1;
			if (call === 1) return accepting()(body);
			return new Response("moov atom not at front of file", { status: 422 });
		});

		const refusal = await buildMessageBody({
			relayURL,
			userSecretHex,
			message: "",
			attachments: [
				{ filename: "notes.pdf", contentType: "application/pdf", contentBase64: "AAAA" },
				{ filename: "clip.mp4", contentType: "video/mp4", contentBase64: "AAAA" },
			],
		}).catch((error: unknown) => error);

		expect((refusal as AttachmentRefused).refusals.map((one) => one.index)).toEqual([1]);
	});

	test("a store that is only busy is not a refusal, because the same file goes later", async () => {
		serveBlossom(() => new Response("upload rate limit exceeded", { status: 429 }));

		const failure = await buildMessageBody({
			relayURL,
			userSecretHex,
			message: "",
			attachments: [{ filename: "notes.pdf", contentType: "application/pdf", contentBase64: "AAAA" }],
		}).catch((error: unknown) => error);

		expect(failure).not.toBeInstanceOf(AttachmentRefused);
		expect((failure as Error).message).toContain("429");
	});
});

describe("what the caller is told", () => {
	test("a refusal answers 415 and names the attachments by position", async () => {
		const refused = { index: 1, filename: "page.html", status: 415, reason: "disallowed content type: text/html" };
		const refusing = {
			sendMessage: async () => {
				throw new AttachmentRefused([refused]);
			},
		};
		const handler = createOutboundHandler({ buzz: {} } as never, {} as ChatdConfiguration, {
			buzz: refusing,
		} as never);

		const response = await handler(
			new Request("http://127.0.0.1/v1/platform/buzz/person.message.send", {
				method: "POST",
				body: JSON.stringify({
					actor: { kind: "buzz-secret", secret: userSecretHex },
					conversationID: "channel-1",
					body: "here it is",
					attachments: [
						{ filename: "notes.pdf", contentType: "application/pdf", contentBase64: "AAAA" },
						{ filename: "page.html", contentType: "text/html", contentBase64: "AAAA" },
					],
				}),
			}),
		);

		expect(response.status).toBe(415);
		expect((await response.json()) as unknown).toMatchObject({ refusedAttachments: [refused] });
	});
});
