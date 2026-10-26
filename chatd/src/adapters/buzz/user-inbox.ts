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

export type UserHeldInboxItem = {
	id: string;
	body: string;
	isUnread: boolean;
};

export type UserInbox = {
	items: UserInboxItem[];
	held: UserHeldInboxItem[];
};

export const MOST_INBOX_ITEMS = 50;
const mostEventsScannedPerQuery = 200;

export async function listInboxAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	directChannelIDs: string[];
	groupChannelIDs: string[];
	sinceSeconds?: number;
	heldIDs?: string[];
	authTagJSON?: string;
}): Promise<UserInbox> {
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const readerPubkeyHex = relay.pubkeyHex;
		const memberOf = new Set([...request.directChannelIDs, ...request.groupChannelIDs]);
		const since = request.sinceSeconds;
		const [mentioning, direct, threadReplies, heldEvents] = await Promise.all([
			mentionsOf(relay, readerPubkeyHex, since),
			messagesIn(relay, request.directChannelIDs, since),
			repliesToThreadsJoined(relay, readerPubkeyHex, [...memberOf], since),
			messagesWithIDs(relay, request.heldIDs ?? []),
		]);
		const newest = newestReasoned(readerPubkeyHex, memberOf, [
			[mentioning, "mention"],
			[direct, "direct"],
			[threadReplies, "thread_reply"],
		]);
		const held = heldEvents.filter((event) => memberOf.has(firstTagValue(event, "h") ?? ""));
		const messageIDs = [...newest.map(({ event }) => event.id), ...held.map((event) => event.id)];
		const [edited, deleted, readState] = await Promise.all([
			editsTo(relay, messageIDs),
			deletionsTo(relay, messageIDs),
			readStateOf(relay, hexToBytes(request.userSecretHex), readerPubkeyHex),
		]);
		const now = nowSeconds();
		const isUnread = (event: BuzzEvent) => {
			const context = readContextOf(firstTagValue(event, "h") ?? "", threadTagsOf(event).rootEventId);
			return event.created_at > unreadSince(readState.readAtOfContext.get(context), now);
		};
		const bodyOf = (event: BuzzEvent) => edited.get(event.id) ?? event.content;
		return {
			items: newest
				.filter(({ event }) => !deleted.has(event.id))
				.map(({ event, reason }) => ({
					reason,
					isUnread: isUnread(event),
					message: {
						id: event.id,
						conversationID: firstTagValue(event, "h") ?? "",
						parentID: threadTagsOf(event).rootEventId,
						authorPubkeyHex: event.pubkey,
						body: bodyOf(event),
						postedAt: new Date(event.created_at * 1000).toISOString(),
					},
				})),
			held: held
				.filter((event) => !deleted.has(event.id))
				.map((event) => ({ id: event.id, body: bodyOf(event), isUnread: isUnread(event) })),
		};
	});
}

function newestReasoned(
	readerPubkeyHex: string,
	memberOf: Set<string>,
	candidates: [BuzzEvent[], InboxReason][],
): { event: BuzzEvent; reason: InboxReason }[] {
	const reasonOf = new Map<string, { event: BuzzEvent; reason: InboxReason }>();
	for (const [events, reason] of candidates) {
		for (const event of events) {
			if (event.pubkey === readerPubkeyHex || reasonOf.has(event.id)) continue;
			if (!memberOf.has(firstTagValue(event, "h") ?? "")) continue;
			reasonOf.set(event.id, { event, reason });
		}
	}
	return [...reasonOf.values()]
		.sort((first, second) => second.event.created_at - first.event.created_at)
		.slice(0, MOST_INBOX_ITEMS);
}

function mentionsOf(relay: QueryingRelay, readerPubkeyHex: string, since?: number): Promise<BuzzEvent[]> {
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], "#p": [readerPubkeyHex], since, limit: mostEventsScannedPerQuery });
}

async function messagesIn(relay: QueryingRelay, channelIDs: string[], since?: number): Promise<BuzzEvent[]> {
	if (channelIDs.length === 0) return [];
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], "#h": channelIDs, since, limit: mostEventsScannedPerQuery });
}

async function messagesWithIDs(relay: QueryingRelay, messageIDs: string[]): Promise<BuzzEvent[]> {
	if (messageIDs.length === 0) return [];
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], ids: messageIDs, limit: messageIDs.length });
}

async function repliesToThreadsJoined(
	relay: QueryingRelay,
	readerPubkeyHex: string,
	channelIDs: string[],
	since?: number,
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
		since,
		limit: mostEventsScannedPerQuery,
	});
	return replies.filter((event) => rootIDs.has(threadTagsOf(event).rootEventId ?? ""));
}
