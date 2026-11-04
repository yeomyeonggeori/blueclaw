import { describe, expect, test } from "bun:test";
import { MalformedRequest, parsePersonRequest } from "../src/personal/parse.ts";

describe("the time zone a person's message is sent from", () => {
	const actor = { kind: "buzz-secret", secret: "s" };

	test("is the IANA zone the device named", () => {
		expect(parsePersonRequest({ actor, timeZone: "America/Los_Angeles" }).timeZone).toBe("America/Los_Angeles");
	});

	test("is absent when the device named none", () => {
		expect(parsePersonRequest({ actor }).timeZone).toBeUndefined();
	});

	test("is refused when it names no zone, so a misread clock never reaches the agent", () => {
		expect(() => parsePersonRequest({ actor, timeZone: "Mars/Olympus" })).toThrow(MalformedRequest);
	});
});
