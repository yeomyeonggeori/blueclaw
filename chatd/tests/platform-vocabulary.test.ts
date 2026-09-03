import { readdirSync } from "node:fs";

import { expect, test } from "bun:test";
import { messengerPlatformNames } from "@blueclaw/protocol";

const adaptersLeavingWithMattermost = ["mattermost"];

function shippedAdapterNames(): string[] {
	return readdirSync(new URL("../src/adapters/", import.meta.url), { withFileTypes: true })
		.filter((entry) => entry.isDirectory())
		.map((entry) => entry.name)
		.sort();
}

function isDeclaredMessenger(name: string): boolean {
	return messengerPlatformNames.some((declared) => declared === name);
}

test("every messenger chatd adapts is one the protocol declares", () => {
	const undeclared = shippedAdapterNames().filter(
		(name) => !isDeclaredMessenger(name) && !adaptersLeavingWithMattermost.includes(name),
	);

	expect(undeclared).toEqual([]);
});

test("chatd ships the adapter for the messenger this product runs on", () => {
	expect(isDeclaredMessenger("buzz")).toBe(true);
	expect(shippedAdapterNames()).toContain("buzz");
});
