import { describe, expect, test } from "bun:test";
import { deriveBuzzSecret } from "../src/adapters/buzz/identity.ts";
import { pubkeyFromSecret } from "../src/adapters/buzz/user-session.ts";

// internkim's internal/buzzidentity/identity_test.go holds these same two
// vectors, so a change to either derivation fails on the other side.
describe("deriveBuzzSecret agrees with internkim's buzzidentity.Secret", () => {
	test("an address written with spaces and capitals derives the same key as its plain form", () => {
		expect(deriveBuzzSecret("mirror-test-seed", " Alice@Example.com ")).toBe(
			"aa04132487d89014a53b0c1d6378dd99fcea6d503842036ee13602211423164b",
		);
	});

	test("a second seed and address derive their own key", () => {
		expect(deriveBuzzSecret("test-seed-1", "Sample@Example.com")).toBe(
			"133163a288bebb795ba689ff5c32beeba804905ebb8160e245ff28f89263ebdf",
		);
	});

	test("a derived secret is a usable signing key", () => {
		expect(pubkeyFromSecret(deriveBuzzSecret("test-seed-1", "Sample@Example.com"))).toMatch(/^[0-9a-f]{64}$/);
	});
});
