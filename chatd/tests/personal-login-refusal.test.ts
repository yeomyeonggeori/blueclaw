import { afterEach, describe, expect, test } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import type { ChatdConfiguration } from "../src/configuration.ts";
import { createOutboundHandler } from "../src/outbound.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";

const refusalReason = "restricted: not a relay member";
const configuration = {} as ChatdConfiguration;
const actor = { kind: "buzz-secret", secret: "2".repeat(64) };

function startRelayRefusingEveryLogin(): { url: string; stop: () => void } {
	const server = Bun.serve({
		port: 0,
		fetch(request, serverInstance) {
			if (serverInstance.upgrade(request)) return undefined;
			return new Response("expected a websocket", { status: 400 });
		},
		websocket: {
			open(socket) {
				socket.send(JSON.stringify(["AUTH", "challenge"]));
			},
			message(socket, data) {
				const frame = JSON.parse(String(data)) as unknown[];
				if (frame[0] !== "AUTH") return;
				const eventID = (frame[1] as { id: string }).id;
				socket.send(JSON.stringify(["OK", eventID, false, refusalReason]));
			},
		},
	});
	return { url: `ws://localhost:${server.port}`, stop: () => void server.stop(true) };
}

function handlerOver(relayURL: string): (request: Request) => Promise<Response> {
	const adapter = new BuzzAdapter({ relayURL, privateKeyHex: "1".repeat(64), botDisplayName: "internkim" });
	(adapter as unknown as { relay: unknown }).relay = { pubkeyHex: "9".repeat(64) };
	return createOutboundHandler({ buzz: adapter }, configuration, {
		buzz: createBuzzPersonalGateway(adapter, { relayURL }),
	});
}

const realConsoleError = console.error;

afterEach(() => {
	console.error = realConsoleError;
	closeEveryPooledRelay();
});

describe("a person whose key the relay does not admit", () => {
	test("is answered at once with a refusal the caller can read, not left waiting", async () => {
		console.error = () => {};
		const relay = startRelayRefusingEveryLogin();
		const started = performance.now();

		const response = await handlerOver(relay.url)(
			new Request("http://127.0.0.1/v1/platform/buzz/person.dm.ensure", {
				method: "POST",
				body: JSON.stringify({ actor, counterpartExternalIDs: [] }),
			}),
		);
		relay.stop();

		expect(response.status).toBe(401);
		expect(await response.json()).toMatchObject({ reason: "login-refused" });
		expect(performance.now() - started).toBeLessThan(5_000);
	});
});
