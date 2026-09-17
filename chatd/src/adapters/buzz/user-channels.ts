import { canonicalChannelName } from "../../channels.ts";
import { withRelayAs } from "./relay-pool.ts";
import { carriesTag, firstTagValue, type BuzzEvent } from "./types.ts";

const PUT_USER_KIND = 9000;
const CREATE_CHANNEL_KIND = 9007;
const JOIN_REQUEST_KIND = 9021;
const GROUP_METADATA_KIND = 39000;
const GROUP_MEMBERS_KIND = 39002;

export type UserChannelVisibility = "open" | "private";

export type UserChannelSpec = {
	name: string;
	description?: string;
	visibility: UserChannelVisibility;
	memberPubkeyHexes: string[];
};

export type UserOpenChannel = {
	channelID: string;
	name: string;
	description?: string;
};

export function createChannelTags(channelID: string, spec: UserChannelSpec): string[][] {
	const tags = [
		["h", channelID],
		["name", canonicalChannelName(spec.name)],
		["visibility", spec.visibility],
		["channel_type", "stream"],
	];
	const description = spec.description?.trim();
	if (description) tags.push(["about", description]);
	return tags;
}

export async function createChannelAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	spec: UserChannelSpec;
}): Promise<{ channelID: string; name: string; uninvitedPubkeyHexes: string[] }> {
	const channelID = crypto.randomUUID();
	const tags = createChannelTags(channelID, request.spec);
	return withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => {
		await relay.publish(CREATE_CHANNEL_KIND, "", tags);
		const others = new Set(request.spec.memberPubkeyHexes.filter((pubkey) => pubkey !== relay.pubkeyHex));
		const uninvitedPubkeyHexes: string[] = [];
		for (const pubkey of others) {
			try {
				await relay.publish(PUT_USER_KIND, "", [
					["h", channelID],
					["p", pubkey],
				]);
			} catch (refusal) {
				console.warn("a channel member was not added", { channelID, pubkey, refusal: String(refusal) });
				uninvitedPubkeyHexes.push(pubkey);
			}
		}
		return { channelID, name: canonicalChannelName(request.spec.name), uninvitedPubkeyHexes };
	});
}

export function openChannelsToJoin(
	metadataEvents: BuzzEvent[],
	joinedChannelIDs: Set<string>,
): UserOpenChannel[] {
	const latest = new Map<string, BuzzEvent>();
	for (const event of metadataEvents) {
		const channelID = firstTagValue(event, "d");
		if (!channelID) continue;
		const known = latest.get(channelID);
		if (!known || event.created_at > known.created_at) latest.set(channelID, event);
	}
	const channels: UserOpenChannel[] = [];
	for (const [channelID, metadata] of latest) {
		if (joinedChannelIDs.has(channelID)) continue;
		if (carriesTag(metadata, "private") || carriesTag(metadata, "hidden")) continue;
		if (firstTagValue(metadata, "t") === "dm" || firstTagValue(metadata, "archived") === "true") continue;
		channels.push({
			channelID,
			name: firstTagValue(metadata, "name") ?? "",
			description: firstTagValue(metadata, "about"),
		});
	}
	return channels.sort((first, second) => first.name.localeCompare(second.name));
}

export async function listOpenChannelsAsUser(request: {
	relayURL: string;
	userSecretHex: string;
}): Promise<UserOpenChannel[]> {
	return withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => {
		const [metadataEvents, memberships] = await Promise.all([
			relay.query({ kinds: [GROUP_METADATA_KIND], limit: 500 }),
			relay.query({ kinds: [GROUP_MEMBERS_KIND], "#p": [relay.pubkeyHex] }),
		]);
		const joined = new Set(
			memberships.map((event) => firstTagValue(event, "d")).filter((id): id is string => Boolean(id)),
		);
		return openChannelsToJoin(metadataEvents, joined);
	});
}

export async function joinChannelAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
}): Promise<void> {
	await withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => {
		await relay.publish(JOIN_REQUEST_KIND, "", [["h", request.channelID]]);
	});
}
