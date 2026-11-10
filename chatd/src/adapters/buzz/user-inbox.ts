import { hexToBytes } from "nostr-tools/utils";
import type { QueryingRelay } from "./deletions.ts";
import { readContextOf, unreadSince } from "./read-state.ts";
import { withRelayAs } from "./relay-pool.ts";
import { EDIT_MESSAGE_KIND, STREAM_MESSAGE_KIND, firstTagValue, threadTagsOf, type BuzzEvent } from "./types.ts";
import { mentionsOf as mentionedIn } from "./user-mentions.ts";
import { nowSeconds, readStateOf } from "./user-read-state.ts";
import type { UserMessageMatch } from "./user-message-search.ts";
import { deletionsTo, editsTo, latestEditsTo } from "./user-session.ts";

export type InboxReason = "mention" | "direct" | "thread_reply" | "channel";

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
	hasMoreBefore: boolean;
};

type TimeWindow = { since?: number; until?: number };

export const MOST_INBOX_ITEMS = 50;
const mostEventsScannedPerQuery = 200;

export async function listInboxAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	directChannelIDs: string[];
	groupChannelIDs: string[];
	sinceSeconds?: number;
	untilSeconds?: number;
	heldIDs?: string[];
	authTagJSON?: string;
}): Promise<UserInbox> {
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const readerPubkeyHex = relay.pubkeyHex;
		const memberOf = new Set([...request.directChannelIDs, ...request.groupChannelIDs]);
		const window = { since: request.sinceSeconds, until: request.untilSeconds };
		const [mentioningByEdit, mentioning, direct, threadReplies, channel, heldEvents] = await Promise.all([
			mentionsByEditOf(relay, readerPubkeyHex, window),
			mentionsOf(relay, readerPubkeyHex, window),
			messagesIn(relay, request.directChannelIDs, window),
			repliesToThreadsJoined(relay, readerPubkeyHex, [...memberOf], window),
			messagesIn(relay, request.groupChannelIDs, window),
			messagesWithIDs(relay, request.heldIDs ?? []),
		]);
		const { newest, hasMoreBefore } = newestReasoned(readerPubkeyHex, memberOf, [
			[mentioningByEdit, "mention"],
			[mentioning, "mention"],
			[direct, "direct"],
			[threadReplies, "thread_reply"],
			[channel, "channel"],
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
			hasMoreBefore,
		};
	});
}

function newestReasoned(
	readerPubkeyHex: string,
	memberOf: Set<string>,
	candidates: [BuzzEvent[], InboxReason][],
): { newest: { event: BuzzEvent; reason: InboxReason }[]; hasMoreBefore: boolean } {
	const reasonOf = new Map<string, { event: BuzzEvent; reason: InboxReason }>();
	for (const [events, reason] of candidates) {
		for (const event of events) {
			if (event.pubkey === readerPubkeyHex || reasonOf.has(event.id)) continue;
			if (!memberOf.has(firstTagValue(event, "h") ?? "")) continue;
			reasonOf.set(event.id, { event, reason });
		}
	}
	const reasoned = [...reasonOf.values()].sort((first, second) => second.event.created_at - first.event.created_at);
	const anyScanFilled = candidates.some(([events]) => events.length >= mostEventsScannedPerQuery);
	return {
		newest: reasoned.slice(0, MOST_INBOX_ITEMS),
		hasMoreBefore: reasoned.length > MOST_INBOX_ITEMS || anyScanFilled,
	};
}

function mentionsOf(relay: QueryingRelay, readerPubkeyHex: string, window: TimeWindow): Promise<BuzzEvent[]> {
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], "#p": [readerPubkeyHex], ...window, limit: mostEventsScannedPerQuery });
}

async function mentionsByEditOf(relay: QueryingRelay, readerPubkeyHex: string, window: TimeWindow): Promise<BuzzEvent[]> {
	const edits = await relay.query({ kinds: [EDIT_MESSAGE_KIND], "#p": [readerPubkeyHex], ...window, limit: mostEventsScannedPerQuery });
	const editedIDs = [...new Set(edits.map((edit) => firstTagValue(edit, "e") ?? "").filter((messageID) => messageID !== ""))];
	const latest = await latestEditsTo(relay, editedIDs);
	const stillMentioning = [...latest].filter(([, edit]) => mentionedIn(edit).pubkeyHexes.includes(readerPubkeyHex));
	return messagesWithIDs(relay, stillMentioning.map(([messageID]) => messageID));
}

async function messagesIn(relay: QueryingRelay, channelIDs: string[], window: TimeWindow): Promise<BuzzEvent[]> {
	if (channelIDs.length === 0) return [];
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], "#h": channelIDs, ...window, limit: mostEventsScannedPerQuery });
}

async function messagesWithIDs(relay: QueryingRelay, messageIDs: string[]): Promise<BuzzEvent[]> {
	if (messageIDs.length === 0) return [];
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], ids: messageIDs, limit: messageIDs.length });
}

async function repliesToThreadsJoined(
	relay: QueryingRelay,
	readerPubkeyHex: string,
	channelIDs: string[],
	window: TimeWindow,
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
		...window,
		limit: mostEventsScannedPerQuery,
	});
	return replies.filter((event) => rootIDs.has(threadTagsOf(event).rootEventId ?? ""));
}
