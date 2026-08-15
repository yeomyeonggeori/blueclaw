import { describe, expect, test } from "bun:test";
import { isServedByTheRelay } from "../src/adapters/buzz/blossom.ts";

const relayURL = "ws://127.0.0.1:3000";

describe("deciding whether the relay this session talks to holds a file", () => {
	test("a file it uploaded itself is its own", () => {
		expect(isServedByTheRelay("http://127.0.0.1:3000/media/9f2c.pdf", relayURL)).toBe(true);
	});

	test("an import that wrote localhost named the same machine", () => {
		expect(isServedByTheRelay("http://localhost:3000/media/9f2c.pdf", relayURL)).toBe(true);
	});

	test("and so did one that wrote the ipv6 loopback", () => {
		expect(isServedByTheRelay("http://[::1]:3000/media/9f2c.pdf", relayURL)).toBe(true);
	});

	test("another port on this machine is another service", () => {
		expect(isServedByTheRelay("http://127.0.0.1:5432/media/9f2c.pdf", relayURL)).toBe(false);
	});

	test("somewhere else on the network is not the relay", () => {
		expect(isServedByTheRelay("http://192.168.1.4:3000/media/9f2c.pdf", relayURL)).toBe(false);
		expect(isServedByTheRelay("http://169.254.169.254/latest/meta-data", relayURL)).toBe(false);
	});

	test("a host that merely starts the same way is not the relay", () => {
		expect(isServedByTheRelay("http://127.0.0.1:3000.example.test/media/a", relayURL)).toBe(false);
	});

	test("a relay reached over tls is named by its own host, not by loopback", () => {
		const behindTLS = "wss://relay.example.test";
		expect(isServedByTheRelay("https://relay.example.test/media/9f2c.pdf", behindTLS)).toBe(true);
		expect(isServedByTheRelay("http://localhost:3000/media/9f2c.pdf", behindTLS)).toBe(false);
	});

	test("something that is not an address at all is not the relay", () => {
		expect(isServedByTheRelay("/media/9f2c.pdf", relayURL)).toBe(false);
		expect(isServedByTheRelay("", relayURL)).toBe(false);
	});
});
