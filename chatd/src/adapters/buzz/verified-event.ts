import { verifyEvent } from "nostr-tools/pure";
import type { BuzzEvent } from "./types.ts";

function hasEventShape(value: unknown): value is BuzzEvent {
	if (typeof value !== "object" || value === null) return false;
	if (!("id" in value) || typeof value.id !== "string") return false;
	if (!("pubkey" in value) || typeof value.pubkey !== "string") return false;
	if (!("sig" in value) || typeof value.sig !== "string") return false;
	if (!("content" in value) || typeof value.content !== "string") return false;
	if (!("created_at" in value) || typeof value.created_at !== "number") return false;
	if (!("kind" in value) || typeof value.kind !== "number") return false;
	if (!("tags" in value) || !Array.isArray(value.tags)) return false;
	return value.tags.every((tag) => Array.isArray(tag) && tag.every((item) => typeof item === "string"));
}

export function verifiedEvent(value: unknown): BuzzEvent | null {
	if (!hasEventShape(value)) return null;
	return verifyEvent(value) ? value : null;
}
