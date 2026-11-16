import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import type { Server, ServerWebSocket } from "bun";
import { finalizeEvent, generateSecretKey, getPublicKey } from "nostr-tools/pure";
import { bytesToHex } from "nostr-tools/utils";
import { closeEveryPooledRelay } from "../src/adapters/buzz/relay-pool.ts";
import { MalformedRequest } from "../src/personal/parse.ts";
import { createBuzzPersonalGateway } from "../src/personal/buzz.ts";
import { personCapabilities } from "../src/personal/capabilities.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

type Filter = { kinds?: number[]; authors?: string[] };

const profileKind = 0;
const kept: BuzzEvent[] = [];
const forgotten: string[] = [];
let refusesProfileReads = false;
const memberSecret = generateSecretKey();
const memberPubkey = getPublicKey(memberSecret);
const actor = { kind: "buzz-secret", secret: bytesToHex(memberSecret) };

function wants(filter: Filter, event: BuzzEvent): boolean {
	if (filter.kinds && !filter.kinds.includes(event.kind)) return false;
	return !filter.authors || filter.authors.includes(event.pubkey);
}

function answer(socket: ServerWebSocket<unknown>, raw: string): void {
	const [frameType, ...rest] = JSON.parse(raw) as [string, ...unknown[]];
	if (frameType === "AUTH" || frameType === "EVENT") {
		const event = rest[0] as BuzzEvent;
		if (frameType === "EVENT") kept.push(event);
		socket.send(JSON.stringify(["OK", event.id, true, ""]));
		return;
	}
	if (frameType !== "REQ") return;
	const [subscriptionID, ...filters] = rest as [string, ...Filter[]];
	if (refusesProfileReads && filters.some((filter) => filter.kinds?.includes(profileKind))) {
		socket.send(JSON.stringify(["CLOSED", subscriptionID, "rate-limited: too many concurrent requests"]));
		return;
	}
	for (const event of kept.filter((candidate) => filters.some((filter) => wants(filter, candidate)))) {
		socket.send(JSON.stringify(["EVENT", subscriptionID, event]));
	}
	socket.send(JSON.stringify(["EOSE", subscriptionID]));
}

function heldProfile(content: Record<string, unknown>, createdAt: number): void {
	kept.push(
		finalizeEvent({ kind: profileKind, created_at: createdAt, tags: [], content: JSON.stringify(content) }, memberSecret) as BuzzEvent,
	);
}

function latestProfile(): Record<string, unknown> {
	const latest = kept
		.filter((event) => event.kind === profileKind && event.pubkey === memberPubkey)
		.sort((first, second) => second.created_at - first.created_at)[0];
	return JSON.parse(latest?.content ?? "{}") as Record<string, unknown>;
}

let server: Server<unknown>;
let relayURL = "";
let pictureURL = "";

async function setPicture(mediaURL: string): Promise<unknown> {
	const gateway = createBuzzPersonalGateway({ forgetProfile: (pubkey: string) => forgotten.push(pubkey) } as never, { relayURL });
	return await personCapabilities["person.profile.picture.set"]?.(gateway, { actor, mediaURL }, "");
}

beforeAll(() => {
	server = Bun.serve({
		port: 0,
		fetch(request, serving) {
			if (serving.upgrade(request, { data: undefined })) return undefined;
			return new Response("a relay speaks websocket", { status: 400 });
		},
		websocket: {
			open(socket) {
				socket.send(JSON.stringify(["AUTH", "challenge"]));
			},
			message(socket, raw) {
				answer(socket, String(raw));
			},
		},
	});
	relayURL = `ws://127.0.0.1:${server.port}`;
	pictureURL = `http://127.0.0.1:${server.port}/media/${"a".repeat(64)}.jpg`;
});

beforeEach(() => {
	kept.length = 0;
	forgotten.length = 0;
	refusesProfileReads = false;
});

afterAll(() => {
	closeEveryPooledRelay();
	server.stop(true);
});

describe("the picture a person is known by", () => {
	test("is published on their own profile beside the name and everything else it held", async () => {
		heldProfile({ name: "이샘플", display_name: "이샘플", about: "sample" }, Math.floor(Date.now() / 1000) - 60);

		const answered = await setPicture(pictureURL);

		expect(answered).toEqual({ externalID: memberPubkey, avatarURL: pictureURL });
		expect(latestProfile()).toEqual({ name: "이샘플", display_name: "이샘플", about: "sample", picture: pictureURL });
	});

	test("replaces the picture the profile held, and the adapter forgets the profile it remembered", async () => {
		heldProfile({ name: "이샘플", picture: "https://example.com/old.png" }, Math.floor(Date.now() / 1000) - 60);

		await setPicture(pictureURL);

		expect(latestProfile()).toEqual({ name: "이샘플", picture: pictureURL });
		expect(forgotten).toEqual([memberPubkey]);
	});

	test("is not set when the profile it belongs on could not be read, so the name it held is not lost", async () => {
		heldProfile({ name: "이샘플" }, Math.floor(Date.now() / 1000) - 60);
		refusesProfileReads = true;

		await expect(setPicture(pictureURL)).rejects.toThrow("could not be read");

		expect(latestProfile()).toEqual({ name: "이샘플" });
		expect(forgotten).toEqual([]);
	});

	test("is refused when another store holds it, and nothing is published", async () => {
		heldProfile({ name: "이샘플" }, Math.floor(Date.now() / 1000) - 60);

		await expect(setPicture("https://elsewhere.test/face.png")).rejects.toBeInstanceOf(MalformedRequest);

		expect(latestProfile()).toEqual({ name: "이샘플" });
	});
});
