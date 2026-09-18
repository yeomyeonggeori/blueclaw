import { canonicalChannelName } from "../../channels.ts";
import { withRelayAs } from "./relay-pool.ts";
import { carriesTag, firstTagValue, type BuzzEvent } from "./types.ts";

const PUT_USER_KIND = 9000;
const CREATE_CHANNEL_KIND = 9007;
const JOIN_REQUEST_KIND = 9021;
const LEAVE_REQUEST_KIND = 9022;
const DELETE_CHANNEL_KIND = 9008;
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
		const uninvitedPubkeyHexes = await addMembers(relay, channelID, request.spec.memberPubkeyHexes);
		return { channelID, name: canonicalChannelName(request.spec.name), uninvitedPubkeyHexes };
	});
}

export async function addChannelMembersAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	memberPubkeyHexes: string[];
}): Promise<{ uninvitedPubkeyHexes: string[] }> {
	return withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => ({
		uninvitedPubkeyHexes: await addMembers(relay, request.channelID, request.memberPubkeyHexes),
	}));
}

export class LastOwnerCannotLeave extends Error {
	readonly reason = "last-owner";

	constructor(channelID: string) {
		super(`the only owner of channel ${channelID} cannot leave it`);
		this.name = "LastOwnerCannotLeave";
	}
}

export type UserChannelRole = "owner" | "admin" | "member";

export class OwnershipPartlyHandedOver extends Error {
	readonly reason = "still-an-owner";

	constructor(channelID: string) {
		super(`the new owner of channel ${channelID} is set, but the old one was not stepped down`);
		this.name = "OwnershipPartlyHandedOver";
	}
}

export class NotChannelOwner extends Error {
	readonly reason = "not-owner";

	constructor(channelID: string) {
		super(`only an owner of channel ${channelID} may do this`);
		this.name = "NotChannelOwner";
	}
}

export function rolesOnRoster(roster: BuzzEvent | undefined): Map<string, UserChannelRole> {
	const roles = new Map<string, UserChannelRole>();
	for (const tag of roster?.tags ?? []) {
		if (tag[0] !== "p" || typeof tag[1] !== "string") continue;
		roles.set(tag[1], tag[3] === "owner" || tag[3] === "admin" ? tag[3] : "member");
	}
	return roles;
}

export function isLastOwner(roster: BuzzEvent | undefined, pubkeyHex: string): boolean {
	const owners = [...rolesOnRoster(roster)].filter(([, role]) => role === "owner").map(([pubkey]) => pubkey);
	return owners.length === 1 && owners[0] === pubkeyHex;
}

export async function handOverOwnershipAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
	newOwnerPubkeyHex: string;
}): Promise<void> {
	await asAnOwner(request, async (relay) => {
		await relay.publish(PUT_USER_KIND, "", [
			["h", request.channelID],
			["p", request.newOwnerPubkeyHex],
			["role", "owner"],
		]);
		try {
			await relay.publish(PUT_USER_KIND, "", [
				["h", request.channelID],
				["p", relay.pubkeyHex],
				["role", "member"],
			]);
		} catch (refusal) {
			console.warn("an old owner was not stepped down", { channelID: request.channelID, refusal: String(refusal) });
			throw new OwnershipPartlyHandedOver(request.channelID);
		}
	});
}

export async function deleteChannelAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
}): Promise<void> {
	await asAnOwner(request, async (relay) => {
		await relay.publish(DELETE_CHANNEL_KIND, "", [["h", request.channelID]]);
	});
}

async function asAnOwner(
	request: { relayURL: string; userSecretHex: string; channelID: string },
	work: (relay: {
		pubkeyHex: string;
		publish: (kind: number, content: string, tags: string[][]) => Promise<unknown>;
	}) => Promise<void>,
): Promise<void> {
	await withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => {
		const roles = rolesOnRoster(await latestRoster(relay, request.channelID));
		if (roles.get(relay.pubkeyHex) !== "owner") throw new NotChannelOwner(request.channelID);
		await work(relay);
	});
}

async function latestRoster(
	relay: { query: (filter: object) => Promise<BuzzEvent[]> },
	channelID: string,
): Promise<BuzzEvent | undefined> {
	const rosters = await relay.query({ kinds: [GROUP_MEMBERS_KIND], "#d": [channelID] });
	return rosters.sort((first, second) => second.created_at - first.created_at)[0];
}

export async function leaveChannelAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
}): Promise<void> {
	await withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => {
		if (isLastOwner(await latestRoster(relay, request.channelID), relay.pubkeyHex)) {
			throw new LastOwnerCannotLeave(request.channelID);
		}
		await relay.publish(LEAVE_REQUEST_KIND, "", [["h", request.channelID]]);
	});
}

async function addMembers(
	relay: { pubkeyHex: string; publish: (kind: number, content: string, tags: string[][]) => Promise<unknown> },
	channelID: string,
	memberPubkeyHexes: string[],
): Promise<string[]> {
	const others = new Set(memberPubkeyHexes.filter((pubkey) => pubkey !== relay.pubkeyHex));
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
	return uninvitedPubkeyHexes;
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
