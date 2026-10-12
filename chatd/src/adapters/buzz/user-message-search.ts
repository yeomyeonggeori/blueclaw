import { withRelayAs } from "./relay-pool.ts";
import { STREAM_MESSAGE_KIND, threadTagsOf } from "./types.ts";
import { deletionsTo, editsTo } from "./user-session.ts";
import { messageSearchScanLimit, newestVerbatimMatches } from "../../message-search.ts";

export type UserMessageMatch = {
	id: string;
	conversationID: string;
	parentID?: string;
	authorPubkeyHex: string;
	body: string;
	postedAt: string;
};

export async function searchChannelMessagesAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	query: string;
	authTagJSON?: string;
}): Promise<UserMessageMatch[]> {
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const events = await relay.query({
			kinds: [STREAM_MESSAGE_KIND],
			"#h": [request.channelID],
			limit: messageSearchScanLimit,
		});
		const messageIDs = events.map((event) => event.id);
		const [edited, deleted] = await Promise.all([editsTo(relay, messageIDs), deletionsTo(relay, messageIDs)]);
		const readable = events
			.filter((event) => !deleted.has(event.id))
			.map((event) => ({ event, body: edited.get(event.id) ?? event.content }));
		return newestVerbatimMatches(
			readable,
			request.query,
			(message) => message.body,
			(message) => message.event.created_at,
		).map(({ event, body }) => ({
			id: event.id,
			conversationID: request.channelID,
			parentID: threadTagsOf(event).rootEventId,
			authorPubkeyHex: event.pubkey,
			body,
			postedAt: new Date(event.created_at * 1000).toISOString(),
		}));
	});
}
