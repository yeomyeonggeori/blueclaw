import { afterEach, describe, expect, test } from "bun:test";
import { createOutboundHandler } from "../src/outbound.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";
import { createMattermostPersonalGateway } from "../src/personal/mattermost.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";

const baseURL = "https://mattermost.test";
const configuration = {} as ChatdConfiguration;
const adapters = { mattermost: {}, buzz: {} } as never;
const gateways = {
	mattermost: createMattermostPersonalGateway(baseURL),
	buzz: createBuzzPersonalGateway({} as never, {} as never),
};
const realFetch = globalThis.fetch;

type Reply = { status?: number; headers?: Record<string, string>; body?: unknown };

function serve(replyFor: (path: string) => Reply): { url: string; body: string | null }[] {
	const seen: { url: string; body: string | null }[] = [];
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		const url = String(input);
		seen.push({ url, body: typeof init?.body === "string" ? init.body : null });
		const reply = replyFor(new URL(url).pathname);
		return new Response(JSON.stringify(reply.body ?? {}), {
			status: reply.status ?? 200,
			headers: { "Content-Type": "application/json", ...reply.headers },
		});
	}) as typeof fetch;
	return seen;
}

function call(platform: string, capability: string, body: unknown): Promise<Response> {
	const handler = createOutboundHandler(adapters, configuration, gateways);
	return handler(
		new Request(`http://127.0.0.1/v1/platform/${platform}/${capability}`, {
			method: "POST",
			body: JSON.stringify(body),
		}),
	);
}

afterEach(() => {
	globalThis.fetch = realFetch;
});

describe("what a person hands over is the platform's to say", () => {
	test("a self-hosted messenger asks for a sign-in", async () => {
		const answer = await call("mattermost", "person.credential.requirement", {});

		expect(await answer.json()).toEqual({
			kind: "sign-in",
			fields: [
				{ name: "loginID", label: "Email or username", isSecret: false },
				{ name: "password", label: "Password", isSecret: true },
			],
		});
	});

	test("a keyed messenger asks for a secret instead", async () => {
		const answer = await call("buzz", "person.credential.requirement", {});
		const requirement = (await answer.json()) as { kind: string; fields: { name: string }[] };

		expect(requirement.kind).toBe("secret");
		expect(requirement.fields.map((field) => field.name)).toEqual(["secret"]);
	});

	test("the requirement needs no credential, because it is how you get one", async () => {
		expect((await call("mattermost", "person.credential.requirement", {})).status).toBe(200);
	});
});

describe("a sign-in becomes something durable, and the password is not kept", () => {
	test("the answers are exchanged for a token, and the identity it resolves to comes back", async () => {
		const seen = serve((path) => {
			if (path.endsWith("/users/login")) {
				return { headers: { token: "a-session" }, body: { id: "U1" } };
			}
			if (path.endsWith("/tokens")) return { body: { token: "a-durable-token" } };
			return { body: { id: "U1", username: "sample", first_name: "이", last_name: "샘플" } };
		});

		const answer = await call("mattermost", "person.credential.issue", {
			answers: { loginID: "sample@example.com", password: "a-password" },
		});

		expect(await answer.json()).toEqual({
			credential: { kind: "mattermost-token", secret: "a-durable-token" },
			identity: { externalID: "U1", name: "이 샘플", serverURL: baseURL },
		});
		const carriedThePassword = seen.filter((request) => request.body?.includes("a-password"));
		expect(carriedThePassword.map((request) => new URL(request.url).pathname)).toEqual([
			"/api/v4/users/login",
		]);
	});

	test("the identity is read with the durable credential, proving it works before anything is stored", async () => {
		const seen = serve((path) => {
			if (path.endsWith("/users/login")) {
				return { headers: { token: "a-session" }, body: { id: "U1" } };
			}
			if (path.endsWith("/tokens")) return { body: { token: "a-durable-token" } };
			return { body: { id: "U1", username: "sample", first_name: "", last_name: "" } };
		});

		await call("mattermost", "person.credential.issue", {
			answers: { loginID: "sample@example.com", password: "a-password" },
		});

		expect(seen.map((request) => new URL(request.url).pathname)).toEqual([
			"/api/v4/users/login",
			"/api/v4/users/U1/tokens",
			"/api/v4/users/me",
		]);
	});

	test("a wrong password is the person's problem, not the gateway's", async () => {
		serve(() => ({ status: 401 }));

		const answer = await call("mattermost", "person.credential.issue", {
			answers: { loginID: "sample@example.com", password: "wrong" },
		});

		expect(answer.status).toBe(401);
	});

	test("a messenger that forbids personal tokens says what an administrator must turn on", async () => {
		serve((path) =>
			path.endsWith("/users/login")
				? { headers: { token: "a-session" }, body: { id: "U1" } }
				: { status: 403 },
		);

		const answer = await call("mattermost", "person.credential.issue", {
			answers: { loginID: "sample@example.com", password: "a-password" },
		});

		expect(answer.status).toBe(401);
		expect((await answer.json()) as { error: string }).toEqual({
			error:
				"this messenger does not let a person mint their own access token; an administrator has to enable personal access tokens",
		});
	});

	test("a request carrying no answers is malformed rather than a failed sign-in", async () => {
		expect((await call("mattermost", "person.credential.issue", {})).status).toBe(400);
	});
});
