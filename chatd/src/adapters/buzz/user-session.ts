import { getPublicKey } from "nostr-tools/pure";
import { createBuzzRelayClient } from "./relay-client.ts";
import { imetaTag, uploadBlob } from "./blossom.ts";
import { firstTagValue, type BuzzEvent } from "./types.ts";

export type UserDirectMessageAttachment = {
	contentBase64: string;
	filename: string;
	contentType: string;
};

const STREAM_MESSAGE_KIND = 9;
const EDIT_MESSAGE_KIND = 40003;
const DELETE_MESSAGE_KIND = 9005;
const REACTION_KIND = 7;
const DM_OPEN_KIND = 41010;
const GROUP_METADATA_KIND = 39000;
const GROUP_MEMBERS_KIND = 39002;
const PROFILE_KIND = 0;

export type UserConversation = {
	channelID: string;
	name: string;
	isDM: boolean;
	avatarURL?: string;
};

function hexToBytes(hex: string): Uint8Array {
	const bytes = new Uint8Array(hex.length / 2);
	for (let index = 0; index < bytes.length; index++) {
		bytes[index] = Number.parseInt(hex.slice(index * 2, index * 2 + 2), 16);
	}
	return bytes;
}

export function pubkeyFromSecret(userSecretHex: string): string {
	return getPublicKey(hexToBytes(userSecretHex));
}

async function fetchProfileAsUser(
	relay: { query: (filter: object) => Promise<BuzzEvent[]> },
	pubkey: string,
): Promise<{ name?: string; picture?: string }> {
	const events = await relay.query({ kinds: [PROFILE_KIND], authors: [pubkey], limit: 5 });
	const latest = events.sort((first, second) => second.created_at - first.created_at)[0];
	if (!latest?.content) return {};
	try {
		const parsed = JSON.parse(latest.content) as {
			name?: string;
			display_name?: string;
			picture?: string;
		};
		return { name: parsed.name ?? parsed.display_name, picture: parsed.picture };
	} catch {
		return {};
	}
}

export async function listUserConversations(
	relayURL: string,
	userSecretHex: string,
): Promise<UserConversation[]> {
	const relay = createBuzzRelayClient(relayURL, userSecretHex);
	try {
		await relay.connect();
		const userPubkeyHex = relay.pubkeyHex;
		const memberships = await relay.query({ kinds: [GROUP_MEMBERS_KIND], "#p": [userPubkeyHex] });
		const channelIDs = [
			...new Set(
				memberships.map((event) => firstTagValue(event, "d")).filter((id): id is string => Boolean(id)),
			),
		];
		if (channelIDs.length === 0) return [];
		const metadataEvents = await relay.query({ kinds: [GROUP_METADATA_KIND], "#d": channelIDs });
		const latestMetadata = new Map<string, BuzzEvent>();
		for (const event of metadataEvents) {
			const channelID = firstTagValue(event, "d");
			if (!channelID) continue;
			const known = latestMetadata.get(channelID);
			if (!known || event.created_at > known.created_at) latestMetadata.set(channelID, event);
		}
		const conversations: UserConversation[] = [];
		for (const channelID of channelIDs) {
			const metadata = latestMetadata.get(channelID);
			const isDM = metadata ? firstTagValue(metadata, "t") === "dm" : false;
			if (isDM) {
				const participants = metadata
					? metadata.tags.filter((tag) => tag[0] === "p").map((tag) => tag[1])
					: [];
				const counterpart = participants.find((pubkey) => pubkey !== userPubkeyHex);
				const profile = counterpart ? await fetchProfileAsUser(relay, counterpart) : {};
				conversations.push({
					channelID,
					name: profile.name ?? counterpart?.slice(0, 8) ?? "",
					isDM: true,
					avatarURL: profile.picture,
				});
			} else {
				conversations.push({
					channelID,
					name: metadata ? (firstTagValue(metadata, "name") ?? "") : "",
					isDM: false,
				});
			}
		}
		return conversations;
	} finally {
		relay.disconnect();
	}
}

// A person replies only to a thread's root from here, and NIP-10 marks a direct
// reply with the root it answers; the reply marker names the parent of a reply
// to a reply, which this has no way to express.
export function channelMessageTags(
	channelID: string,
	mediaTags: string[][],
	extraTags: string[][] | undefined,
	replyToRootId: string | undefined,
): string[][] {
	const tags: string[][] = [["h", channelID], ...mediaTags, ...(extraTags ?? [])];
	if (replyToRootId) tags.push(["e", replyToRootId, "", "root"]);
	return tags;
}

export async function sendChannelMessageAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	message: string;
	attachments?: UserDirectMessageAttachment[];
	replyToRootId?: string;
	extraTags?: string[][];
	authTagJSON?: string;
}): Promise<string> {
	const { body, mediaTags } = await buildMessageBody(request);
	const relay = createBuzzRelayClient(request.relayURL, request.userSecretHex, request.authTagJSON);
	try {
		await relay.connect();
		const event = await relay.publish(
			STREAM_MESSAGE_KIND,
			body,
			channelMessageTags(request.channelID, mediaTags, request.extraTags, request.replyToRootId),
		);
		return event.id;
	} finally {
		relay.disconnect();
	}
}

export async function editChannelMessageAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	targetEventId: string;
	message: string;
	extraTags?: string[][];
	authTagJSON?: string;
}): Promise<string> {
	const relay = createBuzzRelayClient(request.relayURL, request.userSecretHex, request.authTagJSON);
	try {
		await relay.connect();
		const tags: string[][] = [["h", request.channelID], ["e", request.targetEventId], ...(request.extraTags ?? [])];
		const event = await relay.publish(EDIT_MESSAGE_KIND, request.message, tags);
		return event.id;
	} finally {
		relay.disconnect();
	}
}

export async function deleteChannelMessageAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	targetEventId: string;
	extraTags?: string[][];
	authTagJSON?: string;
}): Promise<void> {
	const relay = createBuzzRelayClient(request.relayURL, request.userSecretHex, request.authTagJSON);
	try {
		await relay.connect();
		const tags: string[][] = [["h", request.channelID], ["e", request.targetEventId], ...(request.extraTags ?? [])];
		await relay.publish(DELETE_MESSAGE_KIND, "", tags);
	} finally {
		relay.disconnect();
	}
}

export async function addReactionAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	targetEventId: string;
	emoji: string;
	extraTags?: string[][];
	authTagJSON?: string;
}): Promise<void> {
	const relay = createBuzzRelayClient(request.relayURL, request.userSecretHex, request.authTagJSON);
	try {
		await relay.connect();
		const tags: string[][] = [["e", request.targetEventId], ["h", request.channelID], ...(request.extraTags ?? [])];
		await relay.publish(REACTION_KIND, request.emoji, tags);
	} finally {
		relay.disconnect();
	}
}

export type UserDirectMessageSend = {
	relayURL: string;
	userSecretHex: string;
	counterpartPubkeyHex: string;
	message: string;
	attachments?: UserDirectMessageAttachment[];
};

export type UserDirectMessageChannel = {
	channelID: string;
	userPubkeyHex: string;
};

export async function ensureUserDirectMessageChannel(
	relayURL: string,
	userSecretHex: string,
	counterpartPubkeyHex: string,
): Promise<UserDirectMessageChannel> {
	const relay = createBuzzRelayClient(relayURL, userSecretHex);
	try {
		await relay.connect();
		const existingChannelID = await findDirectMessageChannelID(relay, relay.pubkeyHex, counterpartPubkeyHex);
		if (existingChannelID) {
			return { channelID: existingChannelID, userPubkeyHex: relay.pubkeyHex };
		}
		const acknowledgement = await relay.publishForAcknowledgement(DM_OPEN_KIND, "", [
			["p", counterpartPubkeyHex],
		]);
		const openedChannelID = channelIDFromAcknowledgement(acknowledgement);
		if (!openedChannelID) {
			throw new Error("relay did not return a direct message channel");
		}
		return { channelID: openedChannelID, userPubkeyHex: relay.pubkeyHex };
	} finally {
		relay.disconnect();
	}
}

export async function sendDirectMessageAsUser(request: UserDirectMessageSend): Promise<string> {
	const channel = await ensureUserDirectMessageChannel(
		request.relayURL,
		request.userSecretHex,
		request.counterpartPubkeyHex,
	);
	const { body, mediaTags } = await buildMessageBody(request);
	const relay = createBuzzRelayClient(request.relayURL, request.userSecretHex);
	try {
		await relay.connect();
		const event = await relay.publish(STREAM_MESSAGE_KIND, body, [
			["h", channel.channelID],
			["p", request.counterpartPubkeyHex],
			...mediaTags,
		]);
		return event.id;
	} finally {
		relay.disconnect();
	}
}

async function buildMessageBody(request: {
	relayURL: string;
	userSecretHex: string;
	message: string;
	attachments?: UserDirectMessageAttachment[];
}): Promise<{ body: string; mediaTags: string[][] }> {
	const attachments = request.attachments ?? [];
	if (attachments.length === 0) return { body: request.message, mediaTags: [] };
	const mediaTags: string[][] = [];
	const bodyParts: string[] = request.message.trim() === "" ? [] : [request.message];
	for (const attachment of attachments) {
		const content = new Uint8Array(Buffer.from(attachment.contentBase64, "base64"));
		const blob = await uploadBlob(request.relayURL, request.userSecretHex, content, attachment.contentType);
		const label = attachment.filename.trim() || (isImageType(attachment.contentType) ? "image" : "file");
		bodyParts.push(isImageType(attachment.contentType) ? `![${label}](${blob.url})` : `[${label}](${blob.url})`);
		mediaTags.push(imetaTag(blob));
	}
	return { body: bodyParts.join("\n"), mediaTags };
}

function isImageType(contentType: string): boolean {
	return contentType.startsWith("image/");
}

async function findDirectMessageChannelID(
	relay: { query: (filter: object) => Promise<BuzzEvent[]> },
	userPubkeyHex: string,
	counterpartPubkeyHex: string,
): Promise<string | undefined> {
	const metadataEvents = await relay.query({ kinds: [GROUP_METADATA_KIND], "#p": [counterpartPubkeyHex] });
	const latestByChannel = new Map<string, BuzzEvent>();
	for (const event of metadataEvents) {
		const channelID = firstTagValue(event, "d");
		if (!channelID) continue;
		const known = latestByChannel.get(channelID);
		if (!known || event.created_at > known.created_at) latestByChannel.set(channelID, event);
	}
	for (const [channelID, event] of latestByChannel) {
		if (firstTagValue(event, "t") !== "dm") continue;
		const participants = event.tags.filter((tag) => tag[0] === "p").map((tag) => tag[1]);
		if (participants.length !== 2) continue;
		if (participants.includes(userPubkeyHex) && participants.includes(counterpartPubkeyHex)) {
			return channelID;
		}
	}
	return undefined;
}

function channelIDFromAcknowledgement(acknowledgement: string): string | undefined {
	const payloadStart = acknowledgement.indexOf("{");
	if (payloadStart < 0) return undefined;
	try {
		const payload: unknown = JSON.parse(acknowledgement.slice(payloadStart));
		if (typeof payload === "object" && payload !== null && "channel_id" in payload) {
			const { channel_id: channelID } = payload as { channel_id: unknown };
			return typeof channelID === "string" && channelID.trim() !== "" ? channelID : undefined;
		}
	} catch {
		return undefined;
	}
	return undefined;
}
