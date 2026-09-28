import { getPublicKey } from "nostr-tools/pure";
import { hexToBytes } from "nostr-tools/utils";
import { v2 as nip44 } from "nostr-tools/nip44";
import { takenBackIDs, type QueryingRelay } from "./deletions.ts";
import { withRelayAs } from "./relay-pool.ts";
import {
	READ_STATE_CLIENT_ID,
	READ_STATE_HORIZON_SECONDS,
	READ_STATE_KIND,
	READ_STATE_TOPIC,
	countUnread,
	isReadStateEvent,
	mergeReadAt,
	parseReadStateBlob,
	readStateDTag,
	readStateSlotID,
	serializeReadStateBlob,
	slotIDOf,
	unreadSince,
	withContextReadAt,
	type ReadStateBlob,
} from "./read-state.ts";
import { STREAM_MESSAGE_KIND } from "./types.ts";

export const MOST_UNREAD_COUNTED = 100;
const mostTakeBacksReadPerMessage = 2;

export class ReadStateSlotTaken extends Error {
	constructor(slotID: string, clientID: string) {
		super(`read-state slot ${slotID} belongs to client ${clientID}, not this one`);
		this.name = "ReadStateSlotTaken";
	}
}

type ReadState = {
	readAtOfContext: Map<string, number>;
	own: ReadStateBlob | null;
};

export async function markConversationReadAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	readAtSeconds: number;
	authTagJSON?: string;
}): Promise<void> {
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const secret = hexToBytes(request.userSecretHex);
		const pubkeyHex = getPublicKey(secret);
		const state = await readStateOf(relay, secret, pubkeyHex);
		if ((state.readAtOfContext.get(request.channelID) ?? 0) >= request.readAtSeconds) return;
		const updated = withContextReadAt(state.own, request.channelID, request.readAtSeconds, nowSeconds());
		const conversationKey = nip44.utils.getConversationKey(secret, pubkeyHex);
		await relay.publish(READ_STATE_KIND, nip44.encrypt(serializeReadStateBlob(updated), conversationKey), [
			["d", readStateDTag(readStateSlotID(pubkeyHex))],
			["t", READ_STATE_TOPIC],
		]);
	});
}

export async function unreadCountsAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelIDs: string[];
	authTagJSON?: string;
}): Promise<Map<string, number>> {
	if (request.channelIDs.length === 0) return new Map();
	return withRelayAs(request.relayURL, request.userSecretHex, request.authTagJSON, async (relay) => {
		const secret = hexToBytes(request.userSecretHex);
		const pubkeyHex = getPublicKey(secret);
		const { readAtOfContext } = await readStateOf(relay, secret, pubkeyHex);
		const now = nowSeconds();
		const counted = await Promise.all(
			request.channelIDs.map(async (channelID): Promise<[string, number]> => {
				const since = unreadSince(readAtOfContext.get(channelID), now);
				return [channelID, await unreadIn(relay, channelID, pubkeyHex, since)];
			}),
		);
		return new Map(counted);
	});
}

async function unreadIn(
	relay: QueryingRelay,
	channelID: string,
	pubkeyHex: string,
	since: number,
): Promise<number> {
	const messages = await relay.query({
		kinds: [STREAM_MESSAGE_KIND],
		"#h": [channelID],
		since: since + 1,
		limit: MOST_UNREAD_COUNTED,
	});
	const taken = await takenBackIDs(
		relay,
		messages.map((message) => message.id),
		mostTakeBacksReadPerMessage,
	);
	return countUnread(
		messages.filter((message) => !taken.has(message.id)),
		pubkeyHex,
		since,
	);
}

async function readStateOf(relay: QueryingRelay, secret: Uint8Array, pubkeyHex: string): Promise<ReadState> {
	const events = await relay.query({
		kinds: [READ_STATE_KIND],
		authors: [pubkeyHex],
		"#t": [READ_STATE_TOPIC],
		since: nowSeconds() - READ_STATE_HORIZON_SECONDS,
	});
	const conversationKey = nip44.utils.getConversationKey(secret, pubkeyHex);
	const ownSlotID = readStateSlotID(pubkeyHex);
	const blobs: ReadStateBlob[] = [];
	let own: { blob: ReadStateBlob; createdAt: number } | null = null;
	for (const event of events) {
		if (event.pubkey !== pubkeyHex || !isReadStateEvent(event)) continue;
		const blob = decryptBlob(event.content, conversationKey);
		if (!blob) continue;
		blobs.push(blob);
		if (slotIDOf(event) !== ownSlotID) continue;
		if (own === null || event.created_at > own.createdAt) own = { blob, createdAt: event.created_at };
	}
	if (own && own.blob.clientID !== READ_STATE_CLIENT_ID) {
		throw new ReadStateSlotTaken(ownSlotID, own.blob.clientID);
	}
	return { readAtOfContext: mergeReadAt(blobs), own: own?.blob ?? null };
}

function decryptBlob(content: string, conversationKey: Uint8Array): ReadStateBlob | null {
	try {
		return parseReadStateBlob(nip44.decrypt(content, conversationKey));
	} catch {
		return null;
	}
}

function nowSeconds(): number {
	return Math.floor(Date.now() / 1000);
}
