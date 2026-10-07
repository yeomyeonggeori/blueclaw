import { describe, expect, mock, test } from "bun:test";

let opened = 0;
let closed = 0;
const client = {
	pubkeyHex: "a".repeat(64),
	connect: async () => {
		opened += 1;
	},
	disconnect: () => {
		closed += 1;
	},
	publish: async () => ({ id: "e1" }),
	query: async () => [],
	subscribe: () => {},
	publishForAcknowledgement: async () => ({ event: { id: "e1" }, acknowledgement: "" }),
};
mock.module("../src/adapters/buzz/relay-client.ts", () => ({ createBuzzRelayClient: () => client }));

const { withRelayAs, holdRelayAs, closeEveryPooledRelay } = await import("../src/adapters/buzz/relay-pool.ts");

describe("a person's relay connection", () => {
	test("is opened once and reused, not once per message", async () => {
		opened = 0;
		await withRelayAs("wss://relay", "secret-1", undefined, async () => "first");
		await withRelayAs("wss://relay", "secret-1", undefined, async () => "second");
		await withRelayAs("wss://relay", "secret-1", undefined, async () => "third");

		expect(opened).toBe(1);
		closeEveryPooledRelay();
	});

	test("is not shared between two people", async () => {
		opened = 0;
		await withRelayAs("wss://relay", "secret-1", undefined, async () => "mine");
		await withRelayAs("wss://relay", "secret-2", undefined, async () => "theirs");

		expect(opened).toBe(2);
		closeEveryPooledRelay();
	});

	test("outlives a refused request, so the next one does not log in again", async () => {
		opened = 0;
		closed = 0;
		await expect(
			withRelayAs("wss://relay", "secret-1", undefined, async () => {
				throw new Error("rate-limited: quota exceeded");
			}),
		).rejects.toThrow("rate-limited");
		await withRelayAs("wss://relay", "secret-1", undefined, async () => "after");

		expect(closed).toBe(0);
		expect(opened).toBe(1);
		closeEveryPooledRelay();
	});

	test("held by a subscription is the one every request of the same person borrows", async () => {
		opened = 0;
		const held = holdRelayAs("wss://relay", "secret-1", undefined);
		await withRelayAs("wss://relay", "secret-1", undefined, async () => "a request while held");
		held.release();
		held.release();

		expect(opened).toBe(1);
		closeEveryPooledRelay();
	});
});
