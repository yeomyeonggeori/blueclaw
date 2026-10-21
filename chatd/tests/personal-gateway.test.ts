import { afterEach, describe, expect, test } from "bun:test";
import { createOutboundHandler } from "../src/outbound.ts";
import { createMattermostPersonalGateway } from "../src/personal/mattermost.ts";
import { UnsupportedByPlatform } from "../src/personal/gateway.ts";
import { parseActor } from "../src/personal/parse.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const baseURL = "https://mattermost.test";
const configuration = {} as ChatdConfiguration;
const adapters = { mattermost: { botUserId: "bot-1" } } as never;
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

function call(capability: string, body: unknown, through = gateways): Promise<Response> {
	const handler = createOutboundHandler(adapters, configuration, through);
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

	test("a conversation says whether it is private, so a reader can be told", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me/teams")) return [{ id: "team-1" }];
			if (url.includes("/channels")) {
				return [
					{ id: "channel-1", display_name: "Town Square", name: "town-square", type: "O" },
					{ id: "channel-2", display_name: "HR", name: "hr", type: "P" },
					{ id: "channel-3", display_name: "", name: "person-a__person-b", type: "D" },
				];
			}
			return [];
		});

		const answer = await (
			await call("person.conversations.list", {
				actor: { kind: "mattermost-token", secret: "the-person-token" },
			})
		).json();

		const privacyOf = new Map<string, boolean>(
			answer.conversations.map((conversation: { id: string; isPrivate: boolean }) => [
				conversation.id,
				conversation.isPrivate,
			]),
		);

		expect(privacyOf.get("channel-1")).toBe(false);
		expect(privacyOf.get("channel-2")).toBe(true);
		expect(privacyOf.get("channel-3")).toBe(true);
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

	test("a direct conversation is called after the person on the other side", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me")) return { id: "person-a", username: "lee", first_name: "", last_name: "" };
			if (url.endsWith("/users/me/teams")) return [{ id: "team-1" }];
			if (url.endsWith("/users/ids")) {
				return [{ id: "person-b", username: "sample", first_name: "예시", last_name: "박" }];
			}
			if (url.includes("/channels")) {
				return [{ id: "channel-1", display_name: "", name: "person-a__person-b", type: "D" }];
			}
			return [];
		});

		const answer = await (
			await call("person.conversations.list", {
				actor: { kind: "mattermost-token", secret: "the-person-token" },
			})
		).json();

		const direct = answer.conversations.find((conversation: { id: string }) => conversation.id === "channel-1");
		expect(direct.name).toBe("예시 박");
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

	test("the link opens the address a browser can reach, not the loopback chatd calls", async () => {
		const loopback = "http://127.0.0.1:8065";
		recordFetches((url) => {
			if (url.includes("/config/client")) return { SiteURL: "https://company.example.test/" };
			if (url.endsWith("/users/me/teams")) return [{ id: "team-1", name: "internkim" }];
			if (url.includes("/channels")) {
				return [{ id: "channel-2", display_name: "Announcements", name: "announcements", type: "O" }];
			}
			return [];
		});

		const answer = await (
			await call(
				"person.conversations.list",
				{ actor: { kind: "mattermost-token", secret: "the-person-token" } },
				{ mattermost: createMattermostPersonalGateway(loopback) },
			)
		).json();

		expect(answer.conversations[0].webURL).toBe(
			"https://company.example.test/internkim/channels/announcements",
		);
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


describe("the conversation with the agent", () => {
	function listing() {
		return call("person.conversations.list", {
			actor: { kind: "mattermost-token", secret: "the-person-token" },
		});
	}

	test("is marked, so a client can call the product by its own name", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me")) return { id: "person-a", username: "lee", first_name: "", last_name: "" };
			if (url.endsWith("/users/me/teams")) return [{ id: "team-1" }];
			if (url.endsWith("/users/ids")) {
				return [
					{ id: "bot-1", username: "internkim", first_name: "Intern", last_name: "Kim" },
					{ id: "person-b", username: "sample", first_name: "예시", last_name: "박" },
				];
			}
			if (url.includes("/channels")) {
				return [
					{ id: "with-the-agent", display_name: "", name: "person-a__bot-1", type: "D" },
					{ id: "with-a-colleague", display_name: "", name: "person-a__person-b", type: "D" },
				];
			}
			return [];
		});

		const answer = await (await listing()).json();
		const conversationOf = (id: string) =>
			answer.conversations.find((conversation: { id: string }) => conversation.id === id);

		expect(conversationOf("with-the-agent").isWithTheAgent).toBe(true);
		expect(conversationOf("with-a-colleague").isWithTheAgent).toBe(false);
	});
});

describe("the people list", () => {
	test("names the agent's account, the one place a client learns which account that is", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me")) return { id: "person-a", username: "lee", first_name: "", last_name: "" };
			if (url.includes("/users?")) {
				return [
					{ id: "bot-1", username: "internkim", first_name: "Intern", last_name: "Kim" },
					{ id: "person-b", username: "sample", first_name: "예시", last_name: "박" },
				];
			}
			return [];
		});

		const answer = await (
			await call("person.people.list", { actor: { kind: "mattermost-token", secret: "the-person-token" } })
		).json();

		expect(answer.agentExternalID).toBe("bot-1");
		expect(answer.people.map((person: { externalID: string }) => person.externalID)).toEqual(["bot-1", "person-b"]);
	});
});

describe("an attachment the messenger gave no media type", () => {
	test("is still known by what its name says it is", async () => {
		recordFetches((url) => {
			if (url.endsWith("/users/me")) return { id: "person-a", username: "lee", first_name: "", last_name: "" };
			if (url.includes("/posts")) {
				return {
					order: ["post-1"],
					posts: {
						"post-1": {
							id: "post-1",
							channel_id: "channel-1",
							user_id: "person-a",
							message: "here",
							create_at: 1,
							metadata: {
								files: [
									{ id: "file-1", name: "1CC4A632-8B7E.PNG", mime_type: "", extension: "png", size: 213000 },
									{ id: "file-2", name: "notes", mime_type: "", size: 10 },
								],
							},
						},
					},
				};
			}
			return [];
		});

		const answer = await (
			await call("person.messages.list", {
				actor: { kind: "mattermost-token", secret: "the-person-token" },
				conversationID: "channel-1",
			})
		).json();

		const attachments = answer.messages[0].attachments;
		expect(attachments[0].contentType).toBe("image/png");
		expect(attachments[1].contentType).toBe("");
	});
});

describe("an operation the platform never does", () => {
	test("is answered as unsupported, with a reason a screen can read", async () => {
		const refusing = {
			mattermost: {
				platform: "mattermost",
				credentialKind: "mattermost-token",
				removeReaction: async () => {
					throw new UnsupportedByPlatform("mattermost", "take a reaction back");
				},
			},
		} as never;

		const answer = await call(
			"person.reaction.remove",
			{
				actor: { kind: "mattermost-token", secret: "token" },
				conversationID: "channel-1",
				messageID: "post-1",
				emoji: "\u{1F440}",
			},
			refusing,
		);

		expect(answer.status).toBe(501);
		expect(await answer.json()).toMatchObject({
			reason: "unsupported-by-platform",
			platform: "mattermost",
		});
	});
});

describe("person.read_state.mark", () => {
	type Mark = { conversationID: string; readAt: string; threadRootID?: string };

	function recordingMarks(): { marks: Mark[]; gateways: never } {
		const marks: Mark[] = [];
		const gateways = {
			mattermost: {
				platform: "mattermost",
				credentialKind: "mattermost-token",
				markConversationRead: async (_actor: unknown, conversationID: string, readAt: Date, threadRootID?: string) => {
					marks.push({ conversationID, readAt: readAt.toISOString(), ...(threadRootID ? { threadRootID } : {}) });
				},
			},
		} as never;
		return { marks, gateways };
	}

	test("records the conversation as read up to the time it names", async () => {
		const { marks, gateways: recording } = recordingMarks();
		const answer = await call(
			"person.read_state.mark",
			{
				actor: { kind: "mattermost-token", secret: "token" },
				conversationID: "channel-1",
				readAt: "2026-09-28T01:05:00Z",
			},
			recording,
		);
		expect(answer.status).toBe(200);
		expect(marks).toEqual([{ conversationID: "channel-1", readAt: "2026-09-28T01:05:00.000Z" }]);
	});

	test("records a thread as read on its own when the mark names the thread's root", async () => {
		const { marks, gateways: recording } = recordingMarks();
		const answer = await call(
			"person.read_state.mark",
			{
				actor: { kind: "mattermost-token", secret: "token" },
				conversationID: "channel-1",
				parentID: "root-1",
				readAt: "2026-09-28T01:05:00Z",
			},
			recording,
		);
		expect(answer.status).toBe(200);
		expect(marks).toEqual([{ conversationID: "channel-1", readAt: "2026-09-28T01:05:00.000Z", threadRootID: "root-1" }]);
	});

	test("refuses a mark with no time, or a time that is not one", async () => {
		const { marks, gateways: recording } = recordingMarks();
		const actor = { kind: "mattermost-token", secret: "token" };
		expect((await call("person.read_state.mark", { actor, conversationID: "channel-1" }, recording)).status).toBe(400);
		expect(
			(await call("person.read_state.mark", { actor, conversationID: "channel-1", readAt: "yesterday" }, recording))
				.status,
		).toBe(400);
		expect(marks).toEqual([]);
	});
});

describe("person.typing.send", () => {
	function recordingTyping(): { typed: string[]; gateways: never } {
		const typed: string[] = [];
		const gateways = {
			mattermost: {
				platform: "mattermost",
				credentialKind: "mattermost-token",
				announceTyping: async (_actor: unknown, conversationID: string) => {
					typed.push(conversationID);
				},
			},
		} as never;
		return { typed, gateways };
	}

	test("announces typing in the conversation it names", async () => {
		const { typed, gateways: recording } = recordingTyping();
		const answer = await call(
			"person.typing.send",
			{ actor: { kind: "mattermost-token", secret: "token" }, conversationID: "channel-1" },
			recording,
		);
		expect(answer.status).toBe(200);
		expect(typed).toEqual(["channel-1"]);
	});

	test("refuses typing that names no conversation", async () => {
		const { typed, gateways: recording } = recordingTyping();
		const answer = await call(
			"person.typing.send",
			{ actor: { kind: "mattermost-token", secret: "token" } },
			recording,
		);
		expect(answer.status).toBe(400);
		expect(typed).toEqual([]);
	});
});

describe("person.inbox.list", () => {
	test("is read as the person asking, and answers with their inbox", async () => {
		const actor = { kind: "mattermost-token", secret: "token" };
		const askedBy: unknown[] = [];
		const item = {
			reason: "mention",
			isUnread: true,
			message: { id: "post-1", conversationID: "channel-1", authorExternalID: "person-a", body: "확인 부탁드려요", postedAt: "2026-10-12T05:00:00.000Z" },
		};
		const gateways = {
			mattermost: {
				platform: "mattermost",
				credentialKind: "mattermost-token",
				listInbox: async (reader: unknown) => {
					askedBy.push(reader);
					return [item];
				},
			},
		} as never;

		const answer = await call("person.inbox.list", { actor }, gateways);

		expect(answer.status).toBe(200);
		expect(askedBy).toEqual([actor]);
		expect(await answer.json()).toEqual({ items: [item] });
	});
});

describe("a person searches the conversation they have open", () => {
	const actor = { kind: "mattermost-token", secret: "token" };

	function searchingGateway(asked: unknown[]) {
		return {
			mattermost: {
				platform: "mattermost",
				credentialKind: "mattermost-token",
				searchMessages: async (searcher: unknown, conversationID: string, query: string) => {
					asked.push({ searcher, conversationID, query });
					return [{ id: "post-1", conversationID, authorExternalID: "person-a", body: "회의록 올렸어요", postedAt: "2026-10-12T05:00:00.000Z" }];
				},
			},
		} as never;
	}

	test("is searched as that person, in that conversation, for what they typed", async () => {
		const asked: unknown[] = [];

		const answer = await call(
			"person.messages.search",
			{ actor, conversationID: "channel-1", query: "회의록" },
			searchingGateway(asked),
		);

		expect(answer.status).toBe(200);
		expect(asked).toEqual([{ searcher: actor, conversationID: "channel-1", query: "회의록" }]);
		expect((await answer.json()).matches.map((match: { id: string }) => match.id)).toEqual(["post-1"]);
	});

	test("is refused when nothing was typed", async () => {
		const asked: unknown[] = [];

		const answer = await call("person.messages.search", { actor, conversationID: "channel-1", query: "  " }, searchingGateway(asked));

		expect(answer.status).toBe(400);
		expect(asked).toEqual([]);
	});
});
