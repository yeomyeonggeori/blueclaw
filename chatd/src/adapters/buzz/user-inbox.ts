import { hexToBytes } from "nostr-tools/utils";
import type { QueryingRelay } from "./deletions.ts";
import { readContextOf, unreadSince } from "./read-state.ts";
import { withRelayAs } from "./relay-pool.ts";
import { STREAM_MESSAGE_KIND, firstTagValue, threadTagsOf, type BuzzEvent } from "./types.ts";
import { nowSeconds, readStateOf } from "./user-read-state.ts";
import type { UserMessageMatch } from "./user-message-search.ts";
import { deletionsTo, editsTo } from "./user-session.ts";

export type InboxReason = "mention" | "direct" | "thread_reply";

export type UserInboxItem = {
	reason: InboxReason;
	message: UserMessageMatch;
	isUnread: boolean;
};

export const MOST_INBOX_ITEMS = 50;
const mostEventsScannedPerQuery = 200;

export async function listInboxAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	directChannelIDs: string[];
	groupChannelIDs: string[];
	authTagJSON?: string;
}): Promise<UserInboxItem[]> {
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const readerPubkeyHex = relay.pubkeyHex;
		const memberOf = new Set([...request.directChannelIDs, ...request.groupChannelIDs]);
		const [mentioning, direct, threadReplies] = await Promise.all([
			mentionsOf(relay, readerPubkeyHex),
			messagesIn(relay, request.directChannelIDs),
			repliesToThreadsJoined(relay, readerPubkeyHex, [...memberOf]),
		]);
		const reasonOf = new Map<string, { event: BuzzEvent; reason: InboxReason }>();
		const candidates: [BuzzEvent[], InboxReason][] = [
			[mentioning, "mention"],
			[direct, "direct"],
			[threadReplies, "thread_reply"],
		];
		for (const [events, reason] of candidates) {
			for (const event of events) {
				if (event.pubkey === readerPubkeyHex || reasonOf.has(event.id)) continue;
				if (!memberOf.has(firstTagValue(event, "h") ?? "")) continue;
				reasonOf.set(event.id, { event, reason });
			}
		}
		const newest = [...reasonOf.values()]
			.sort((first, second) => second.event.created_at - first.event.created_at)
			.slice(0, MOST_INBOX_ITEMS);
		const messageIDs = newest.map(({ event }) => event.id);
		const [edited, deleted, readState] = await Promise.all([
			editsTo(relay, messageIDs),
			deletionsTo(relay, messageIDs),
			readStateOf(relay, hexToBytes(request.userSecretHex), readerPubkeyHex),
		]);
		const now = nowSeconds();
		return newest
			.filter(({ event }) => !deleted.has(event.id))
			.map(({ event, reason }) => {
				const conversationID = firstTagValue(event, "h") ?? "";
				const threadRootID = threadTagsOf(event).rootEventId;
				const readAt = readState.readAtOfContext.get(readContextOf(conversationID, threadRootID));
				return {
					reason,
					isUnread: event.created_at > unreadSince(readAt, now),
					message: {
						id: event.id,
						conversationID,
						parentID: threadRootID,
						authorPubkeyHex: event.pubkey,
						body: edited.get(event.id) ?? event.content,
						postedAt: new Date(event.created_at * 1000).toISOString(),
					},
				};
			});
	});
}

function mentionsOf(relay: QueryingRelay, readerPubkeyHex: string): Promise<BuzzEvent[]> {
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], "#p": [readerPubkeyHex], limit: mostEventsScannedPerQuery });
}

async function messagesIn(relay: QueryingRelay, channelIDs: string[]): Promise<BuzzEvent[]> {
	if (channelIDs.length === 0) return [];
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], "#h": channelIDs, limit: mostEventsScannedPerQuery });
}

async function repliesToThreadsJoined(
	relay: QueryingRelay,
	readerPubkeyHex: string,
	channelIDs: string[],
): Promise<BuzzEvent[]> {
	if (channelIDs.length === 0) return [];
	const own = await relay.query({
		kinds: [STREAM_MESSAGE_KIND],
		authors: [readerPubkeyHex],
		"#h": channelIDs,
		limit: mostEventsScannedPerQuery,
	});
	const rootIDs = new Set(own.map((event) => threadTagsOf(event).rootEventId ?? event.id));
	if (rootIDs.size === 0) return [];
	const replies = await relay.query({
		kinds: [STREAM_MESSAGE_KIND],
		"#e": [...rootIDs],
		limit: mostEventsScannedPerQuery,
	});
	return replies.filter((event) => rootIDs.has(threadTagsOf(event).rootEventId ?? ""));
}
