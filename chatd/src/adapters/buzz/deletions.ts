import { firstTagValue, type BuzzEvent } from "./types.ts";

export const DELETE_MESSAGE_KIND = 9005;
export const REACTION_KIND = 7;

export type QueryingRelay = { query: (filter: object) => Promise<BuzzEvent[]> };

export function targetKindTag(kind: number): string[] {
	return ["k", String(kind)];
}

export function isAbout(event: BuzzEvent, kind: number): boolean {
	return firstTagValue(event, "k") === String(kind);
}

export async function takenBackIDs(
	relay: QueryingRelay,
	eventIDs: string[],
	mostPerEvent: number,
): Promise<Set<string>> {
	if (eventIDs.length === 0) return new Set();
	const events = await relay.query({
		kinds: [DELETE_MESSAGE_KIND],
		"#e": eventIDs,
		limit: eventIDs.length * mostPerEvent,
	});
	const taken = new Set<string>();
	for (const event of events) {
		const eventID = firstTagValue(event, "e");
		if (eventID) taken.add(eventID);
	}
	return taken;
}

export async function takenBackReactionIDs(
	relay: QueryingRelay,
	reactionIDs: string[],
): Promise<Set<string>> {
	return takenBackIDs(relay, reactionIDs, mostTakeBacksReadPerReaction);
}

const mostTakeBacksReadPerReaction = 2;
