import type { QueryingRelay } from "./deletions.ts";
import { messageReadStatusesAsUser, readSummariesAsUser } from "./personal-read.ts";
import { withRelayAs } from "./relay-pool.ts";
import { STREAM_MESSAGE_KIND, firstTagValue, threadTagsOf, type BuzzEvent } from "./types.ts";
import type { UserMessageMatch } from "./user-message-search.ts";
import { deletionsTo, editsTo } from "./user-session.ts";

export type InboxReason = "mention" | "thread_reply";

export type UserInboxItem = {
	reason: InboxReason;
	message: UserMessageMatch;
	isUnread: boolean;
};

export type UserInbox = {
	items: UserInboxItem[];
	hasMoreBefore: boolean;
};

type Reasoned = { event: BuzzEvent; reason: InboxReason };

export const MOST_INBOX_ITEMS = 50;

export async function listInboxAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	untilSeconds?: number;
	authTagJSON?: string;
}): Promise<UserInbox> {
	const summaries = await readSummariesAsUser(request.relayURL, request.userSecretHex);
	const joinedChannelIDs = new Set(summaries.map((summary) => summary.channelID));
	const isFirstPage = request.untilSeconds === undefined;
	const unreadThreadReplyIDs = isFirstPage
		? summaries.flatMap((summary) => summary.unreadThreads.map((thread) => thread.latestReplyID))
		: [];
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const [mentions, threadReplies] = await Promise.all([
			relay.query({ kinds: [STREAM_MESSAGE_KIND], "#p": [relay.pubkeyHex], until: request.untilSeconds, limit: MOST_INBOX_ITEMS }),
			messagesWithIDs(relay, unreadThreadReplyIDs),
		]);
		const mentioning = mentions.filter((event) => event.pubkey !== relay.pubkeyHex && joinedChannelIDs.has(channelOf(event)));
		const reasoned = withoutRepeats([
			...threadReplies.map((event): Reasoned => ({ event, reason: "thread_reply" })),
			...mentioning.map((event): Reasoned => ({ event, reason: "mention" })),
		]);
		const messageIDs = reasoned.map(({ event }) => event.id);
		const [edited, deleted, statuses] = await Promise.all([
			editsTo(relay, messageIDs),
			deletionsTo(relay, messageIDs),
			messageReadStatusesAsUser(request.relayURL, request.userSecretHex, mentioning.map(readSelectorOf)),
		]);
		const isUnread = ({ event, reason }: Reasoned) => reason === "thread_reply" || statuses.get(event.id) === "unread";
		return {
			items: reasoned
				.filter(({ event }) => !deleted.has(event.id))
				.sort((first, second) => second.event.created_at - first.event.created_at)
				.map((entry) => ({
					reason: entry.reason,
					isUnread: isUnread(entry),
					message: matchOf(entry.event, edited.get(entry.event.id) ?? entry.event.content),
				})),
			hasMoreBefore: mentions.length === MOST_INBOX_ITEMS,
		};
	});
}

function withoutRepeats(entries: Reasoned[]): Reasoned[] {
	const seen = new Set<string>();
	return entries.filter(({ event }) => !seen.has(event.id) && seen.add(event.id));
}

function readSelectorOf(event: BuzzEvent) {
	return { channelID: channelOf(event), threadRootID: threadTagsOf(event).rootEventId, messageID: event.id };
}

function matchOf(event: BuzzEvent, body: string): UserMessageMatch {
	return {
		id: event.id,
		conversationID: channelOf(event),
		parentID: threadTagsOf(event).rootEventId,
		authorPubkeyHex: event.pubkey,
		body,
		postedAt: new Date(event.created_at * 1000).toISOString(),
	};
}

function channelOf(event: BuzzEvent): string {
	return firstTagValue(event, "h") ?? "";
}

function messagesWithIDs(relay: QueryingRelay, messageIDs: string[]): Promise<BuzzEvent[]> {
	if (messageIDs.length === 0) return Promise.resolve([]);
	return relay.query({ kinds: [STREAM_MESSAGE_KIND], ids: messageIDs, limit: messageIDs.length });
}
