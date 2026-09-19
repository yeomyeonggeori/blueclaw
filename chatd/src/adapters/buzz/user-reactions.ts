import { getPublicKey } from "nostr-tools/pure";
import { hexToBytes } from "nostr-tools/utils";
import {
	DELETE_MESSAGE_KIND,
	REACTION_KIND,
	takenBackReactionIDs,
	targetKindTag,
	type QueryingRelay,
} from "./deletions.ts";
import { withRelayAs } from "./relay-pool.ts";
import { firstTagValue, type BuzzEvent } from "./types.ts";

const mostReactionsReadPerMessage = 64;
const mostOwnReactionsPerMessage = 64;

export type UserMessageReaction = {
	emoji: string;
	imageURL?: string;
	byPubkeyHexes: string[];
};

export async function addReactionAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	targetEventId: string;
	emoji: string;
	extraTags?: string[][];
	authTagJSON?: string;
}): Promise<void> {
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const tags: string[][] = [["e", request.targetEventId], ["h", request.channelID], ...(request.extraTags ?? [])];
		await relay.publish(REACTION_KIND, request.emoji, tags);
	});
}

export async function removeReactionAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	targetEventId: string;
	emoji: string;
	authTagJSON?: string;
}): Promise<void> {
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const own = await ownReactionTo(
			relay,
			getPublicKey(hexToBytes(request.userSecretHex)),
			request.targetEventId,
			request.emoji,
		);
		if (!own) return;
		await relay.publish(DELETE_MESSAGE_KIND, "", [
			["h", firstTagValue(own, "h") ?? request.channelID],
			["e", own.id],
			targetKindTag(REACTION_KIND),
		]);
	});
}

export async function reactionsTo(
	relay: QueryingRelay,
	messageIDs: string[],
): Promise<Map<string, UserMessageReaction[]>> {
	if (messageIDs.length === 0) return new Map();
	const events = await relay.query({
		kinds: [REACTION_KIND],
		"#e": messageIDs,
		limit: messageIDs.length * mostReactionsReadPerMessage,
	});
	const takenBack = await takenBackReactionIDs(relay, events.map((event) => event.id));
	const byMessage = new Map<string, Map<string, UserMessageReaction>>();
	for (const event of events) {
		if (takenBack.has(event.id)) continue;
		const messageID = firstTagValue(event, "e");
		if (!messageID) continue;
		const grouped = byMessage.get(messageID) ?? new Map<string, UserMessageReaction>();
		const reaction = reactionOf(event);
		const already = grouped.get(reaction.emoji) ?? { ...reaction, byPubkeyHexes: [] };
		if (!already.byPubkeyHexes.includes(event.pubkey)) already.byPubkeyHexes.push(event.pubkey);
		grouped.set(reaction.emoji, already);
		byMessage.set(messageID, grouped);
	}
	return new Map([...byMessage].map(([messageID, grouped]) => [messageID, [...grouped.values()]]));
}

function reactionOf(event: BuzzEvent): UserMessageReaction {
	const named = event.tags.find((tag) => tag[0] === "emoji" && typeof tag[1] === "string");
	if (!named) return { emoji: event.content, byPubkeyHexes: [] };
	return { emoji: named[1] as string, imageURL: named[2], byPubkeyHexes: [] };
}

async function ownReactionTo(
	relay: QueryingRelay,
	authorPubkeyHex: string,
	targetEventId: string,
	emoji: string,
): Promise<BuzzEvent | undefined> {
	const events = await relay.query({
		kinds: [REACTION_KIND],
		authors: [authorPubkeyHex],
		"#e": [targetEventId],
		limit: mostOwnReactionsPerMessage,
	});
	return events
		.filter(
			(event) =>
				event.kind === REACTION_KIND &&
				event.pubkey === authorPubkeyHex &&
				firstTagValue(event, "e") === targetEventId &&
				reactionOf(event).emoji === emoji,
		)
		.sort((first, second) => second.created_at - first.created_at)[0];
}
