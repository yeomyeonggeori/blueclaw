import { createHash } from "node:crypto";
import { firstTagValue, threadTagsOf, type BuzzEvent } from "./types.ts";

export const READ_STATE_KIND = 30078;
export const READ_STATE_TOPIC = "read-state";
export const READ_STATE_HORIZON_SECONDS = 7 * 24 * 60 * 60;
export const READ_STATE_CLIENT_ID = "internkim-web";

const dTagPrefix = "read-state:";
const schemaVersion = 1;
const slotIDPattern = /^[0-9a-f]{32}$/;
const largestTimestamp = 4_294_967_295;
const largestContextBytes = 256;
const mostContexts = 10_000;
const reservedPrefixes = ["ov_", "esc:"];
const escapeMarker = "esc:";

export type ReadStateBlob = {
	clientID: string;
	readAtOfContext: Map<string, number>;
};

export function readStateSlotID(pubkeyHex: string): string {
	return createHash("sha256").update(`${READ_STATE_CLIENT_ID}:${pubkeyHex}`).digest("hex").slice(0, 32);
}

export function readStateDTag(slotID: string): string {
	return `${dTagPrefix}${slotID}`;
}

export function isReadStateEvent(event: BuzzEvent): boolean {
	if (event.kind !== READ_STATE_KIND) return false;
	const dValues = event.tags.filter((tag) => tag[0] === "d").map((tag) => tag[1] ?? "");
	if (dValues.length !== 1) return false;
	const dValue = dValues[0] ?? "";
	if (!dValue.startsWith(dTagPrefix) || !slotIDPattern.test(dValue.slice(dTagPrefix.length))) return false;
	return event.tags.filter((tag) => tag[0] === "t" && tag[1] === READ_STATE_TOPIC).length === 1;
}

export function slotIDOf(event: BuzzEvent): string {
	return (firstTagValue(event, "d") ?? "").slice(dTagPrefix.length);
}

export function parseReadStateBlob(plaintext: string): ReadStateBlob | null {
	let parsed: unknown;
	try {
		parsed = JSON.parse(plaintext);
	} catch {
		return null;
	}
	if (!isRecord(parsed)) return null;
	if (parsed.v !== schemaVersion) return null;
	const clientID = parsed.client_id;
	if (typeof clientID !== "string" || clientID.length === 0 || [...clientID].length > 64) return null;
	const contexts = parsed.contexts;
	if (!isRecord(contexts)) return null;
	const entries = Object.entries(contexts);
	if (entries.length > mostContexts) return null;
	const readAtOfContext = new Map<string, number>();
	for (const [wireKey, value] of entries) {
		if (isOverrideKey(wireKey)) continue;
		if (!isTimestamp(value)) continue;
		const contextID = unescapeContextID(wireKey);
		if (new TextEncoder().encode(contextID).length > largestContextBytes) continue;
		readAtOfContext.set(contextID, value);
	}
	return { clientID, readAtOfContext };
}

export function serializeReadStateBlob(blob: ReadStateBlob): string {
	const contexts: Record<string, number> = {};
	for (const [contextID, readAt] of blob.readAtOfContext) contexts[escapeContextID(contextID)] = readAt;
	return JSON.stringify({ v: schemaVersion, client_id: blob.clientID, contexts });
}

export function mergeReadAt(blobs: ReadStateBlob[]): Map<string, number> {
	const merged = new Map<string, number>();
	for (const blob of blobs) {
		for (const [contextID, readAt] of blob.readAtOfContext) {
			merged.set(contextID, Math.max(merged.get(contextID) ?? 0, readAt));
		}
	}
	return merged;
}

export function withContextReadAt(
	blob: ReadStateBlob | null,
	contextID: string,
	readAt: number,
	nowSeconds: number,
): ReadStateBlob {
	const oldest = nowSeconds - READ_STATE_HORIZON_SECONDS;
	const readAtOfContext = new Map<string, number>();
	for (const [existingID, existingReadAt] of blob?.readAtOfContext ?? []) {
		if (existingReadAt >= oldest) readAtOfContext.set(existingID, existingReadAt);
	}
	readAtOfContext.set(contextID, Math.max(readAtOfContext.get(contextID) ?? 0, readAt));
	return { clientID: READ_STATE_CLIENT_ID, readAtOfContext };
}

export function unreadSince(readAt: number | undefined, nowSeconds: number): number {
	return Math.max(readAt ?? 0, nowSeconds - READ_STATE_HORIZON_SECONDS);
}

export function countUnread(messages: BuzzEvent[], readerPubkeyHex: string, since: number): number {
	return messages.filter(
		(message) =>
			message.created_at > since &&
			message.pubkey !== readerPubkeyHex &&
			threadTagsOf(message).rootEventId === undefined,
	).length;
}

function isOverrideKey(wireKey: string): boolean {
	return wireKey.startsWith("ov_");
}

function escapeContextID(contextID: string): string {
	return reservedPrefixes.some((prefix) => contextID.startsWith(prefix)) ? `${escapeMarker}${contextID}` : contextID;
}

function unescapeContextID(wireKey: string): string {
	return wireKey.startsWith(escapeMarker) ? wireKey.slice(escapeMarker.length) : wireKey;
}

function isTimestamp(value: unknown): value is number {
	return typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= largestTimestamp;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}
