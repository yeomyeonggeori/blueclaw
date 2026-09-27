import type { QueryingRelay } from "./thread-deletion.ts";
import { EDIT_MESSAGE_KIND, STREAM_MESSAGE_KIND, type BuzzEvent } from "./types.ts";

export const catchUpIntervalMilliseconds = 2 * 60 * 1000;

const pageSize = 500;

export async function messagesSentSince(
	relay: QueryingRelay,
	channelIds: string[],
	sinceSeconds: number,
): Promise<BuzzEvent[]> {
	if (channelIds.length === 0) return [];
	const found = new Map<string, BuzzEvent>();
	let untilSeconds: number | undefined;
	for (;;) {
		const page = await pageOfMessages(relay, channelIds, sinceSeconds, untilSeconds);
		const knownBefore = found.size;
		for (const event of page) found.set(event.id, event);
		if (page.length < pageSize || found.size === knownBefore) break;
		untilSeconds = Math.min(...page.map((event) => event.created_at));
	}
	return [...found.values()].sort((earlier, later) => earlier.created_at - later.created_at);
}

async function pageOfMessages(
	relay: QueryingRelay,
	channelIds: string[],
	sinceSeconds: number,
	untilSeconds: number | undefined,
): Promise<BuzzEvent[]> {
	const filter = {
		kinds: [STREAM_MESSAGE_KIND, EDIT_MESSAGE_KIND],
		"#h": channelIds,
		since: sinceSeconds,
		limit: pageSize,
		...(untilSeconds === undefined ? {} : { until: untilSeconds }),
	};
	const { events, complete } = await relay.queryComplete(filter);
	if (!complete) throw new Error(`the relay did not finish listing messages sent since ${sinceSeconds}`);
	return events;
}
