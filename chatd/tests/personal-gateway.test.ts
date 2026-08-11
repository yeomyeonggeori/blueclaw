import { afterEach, describe, expect, test } from "bun:test";
import { createOutboundHandler } from "../src/outbound.ts";
import { createMattermostPersonalGateway } from "../src/personal/mattermost.ts";
import { parseActor } from "../src/personal/parse.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const baseURL = "https://mattermost.test";
const configuration = {} as ChatdConfiguration;
const adapters = { mattermost: {} } as never;
const gateways = { mattermost: createMattermostPersonalGateway(baseURL) };
const realFetch = globalThis.fetch;

type Seen = { url: string; authorization: string | null; method: string; body: string | null };

function recordFetches(reply: (url: string) => unknown): Seen[] {
	const seen: Seen[] = [];
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		const url = String(input);
		seen.push({
			url,
			authorization: new Headers(init?.headers).get("Authorization"),
			method: init?.method ?? "GET",
			body: typeof init?.body === "string" ? init.body : null,
		});
		return Response.json(reply(url));
	}) as typeof fetch;
	return seen;
}

function call(capability: string, body: unknown): Promise<Response> {
	const handler = createOutboundHandler(adapters, configuration, gateways);
	return handler(
		new Request(`http://127.0.0.1/v1/platform/mattermost/${capability}`, {
			method: "POST",
			body: JSON.stringify(body),
		}),
	);
}

afterEach(() => {
	globalThis.fetch = realFetch;
});

describe("parseActor", () => {
	test("takes the actor when it is given", () => {
		expect(parseActor({ actor: { kind: "mattermost-token", secret: "t" } })).toEqual({
			kind: "mattermost-token",
			secret: "t",
		});
	});

	test("accepts the legacy buzz field so admind keeps working", () => {
		expect(parseActor({ userSecretHex: "abc" })).toEqual({ kind: "buzz-secret", secret: "abc" });
	});

	test("refuses a request that names no actor", () => {
		expect(() => parseActor({ conversationID: "c" })).toThrow("missing required field actor");
	});
});

describe("person capabilities", () => {
	test("the actor's own token is what reaches the messenger", async () => {
		const seen = recordFetches(() => []);

		await call("person.conversations.list", {
			actor: { kind: "mattermost-token", secret: "the-person-token" },
		});

		expect(seen[0]?.authorization).toBe("Bearer the-person-token");
		expect(seen[0]?.url).toBe(`${baseURL}/api/v4/users/me/teams`);
	});

	test("a reaction is attributed to the actor, never to a bot", async () => {
		const seen = recordFetches((url) =>
			url.endsWith("/users/me") ? { id: "person-9", username: "kim", first_name: "", last_name: "" } : {},
		);

		await call("person.reaction.add", {
			actor: { kind: "mattermost-token", secret: "the-person-token" },
			conversationID: "channel-1",
			messageID: "post-1",
			emoji: "tada",
		});

		const reaction = seen.find((entry) => entry.url.endsWith("/reactions"));
		expect(reaction?.authorization).toBe("Bearer the-person-token");
		expect(JSON.parse(reaction?.body ?? "{}").user_id).toBe("person-9");
	});

	test("a request with no actor is refused before the messenger is touched", async () => {
		const seen = recordFetches(() => []);

		const response = await call("person.message.send", {
			conversationID: "channel-1",
			body: "hello",
		});

		expect(response.status).toBe(400);
		expect(seen).toHaveLength(0);
	});

	test("a buzz credential is refused by the mattermost gateway", async () => {
		const seen = recordFetches(() => []);

		const response = await call("person.conversations.list", {
			actor: { kind: "buzz-secret", secret: "abc" },
		});

		expect(response.status).toBe(502);
		expect((await response.json()).error).toContain("mattermost needs a mattermost-token");
		expect(seen).toHaveLength(0);
	});

	test("a direct conversation names the two people in it", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me/teams")) return [{ id: "team-1" }];
			if (url.includes("/channels")) {
				return [
					{ id: "channel-1", display_name: "", name: "person-a__person-b", type: "D" },
					{ id: "channel-2", display_name: "Announcements", name: "announcements", type: "O" },
				];
			}
			return [];
		});

		const answer = await (
			await call("person.conversations.list", {
				actor: { kind: "mattermost-token", secret: "the-person-token" },
			})
		).json();

		const direct = answer.conversations.find((conversation: { id: string }) => conversation.id === "channel-1");
		expect(direct.participantExternalIDs).toEqual(["person-a", "person-b"]);
	});

	test("a conversation carries the link that opens it in mattermost", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me/teams")) return [{ id: "team-1", name: "internkim" }];
			if (url.includes("/channels")) {
				return [
					{ id: "channel-1", display_name: "", name: "person-a__person-b", type: "D" },
					{ id: "channel-2", display_name: "Announcements", name: "announcements", type: "O" },
				];
			}
			return [];
		});

		const answer = await (
			await call("person.conversations.list", {
				actor: { kind: "mattermost-token", secret: "the-person-token" },
			})
		).json();

		const linkOf = new Map<string, string>(
			answer.conversations.map((conversation: { id: string; webURL: string }) => [
				conversation.id,
				conversation.webURL,
			]),
		);
		expect(linkOf.get("channel-2")).toBe(`${baseURL}/internkim/channels/announcements`);
		expect(linkOf.get("channel-1")).toBe(`${baseURL}/internkim/channels/person-a__person-b`);
	});

	test("a channel that is not direct names nobody, because its name already says what it is", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me/teams")) return [{ id: "team-1" }];
			if (url.includes("/channels")) {
				return [{ id: "channel-2", display_name: "Announcements", name: "announcements", type: "O" }];
			}
			return [];
		});

		const answer = await (
			await call("person.conversations.list", {
				actor: { kind: "mattermost-token", secret: "the-person-token" },
			})
		).json();

		expect(answer.conversations[0].participantExternalIDs).toBeUndefined();
	});

	test("a platform with no gateway cannot act as a person", async () => {
		const handler = createOutboundHandler(adapters, configuration, {});
		const response = await handler(
			new Request("http://127.0.0.1/v1/platform/mattermost/person.identity", {
				method: "POST",
				body: JSON.stringify({ actor: { kind: "mattermost-token", secret: "t" } }),
			}),
		);

		expect(response.status).toBe(404);
		expect((await response.json()).error).toContain("cannot act as a person");
	});
});
