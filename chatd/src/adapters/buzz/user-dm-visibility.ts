import { withRelayAs } from "./relay-pool.ts";
import { latestRoster } from "./user-channels.ts";
import { channelIDFromAcknowledgement } from "./user-session.ts";
import type { BuzzEvent } from "./types.ts";

const DM_OPEN_KIND = 41010;
const DM_HIDE_KIND = 41012;
const DM_VISIBILITY_KIND = 30622;

type VisibilityRelay = { query: (filter: object) => Promise<BuzzEvent[]> };

export async function hiddenDirectMessageIDs(relay: VisibilityRelay, viewerPubkeyHex: string): Promise<Set<string>> {
	const snapshots = await relay.query({ kinds: [DM_VISIBILITY_KIND], "#p": [viewerPubkeyHex], limit: 1 });
	const latest = snapshots.sort((first, second) => second.created_at - first.created_at)[0];
	return new Set(
		(latest?.tags ?? []).filter((tag) => tag[0] === "h" && typeof tag[1] === "string").map((tag) => tag[1] as string),
	);
}

export async function hideDirectMessageAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
}): Promise<void> {
	await withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => {
		await relay.publishForAcknowledgement(DM_HIDE_KIND, "", [["h", request.channelID]]);
	});
}

export async function revealDirectMessageAsUser(request: {
	relayURL: string;
	userSecretHex: string;
	channelID: string;
}): Promise<void> {
	await withRelayAs(request.relayURL, request.userSecretHex, undefined, async (relay) => {
		if (!(await hiddenDirectMessageIDs(relay, relay.pubkeyHex)).has(request.channelID)) return;
		const roster = await latestRoster(relay, request.channelID);
		const counterparts = (roster?.tags ?? [])
			.filter((tag) => tag[0] === "p" && typeof tag[1] === "string" && tag[1] !== relay.pubkeyHex)
			.map((tag) => ["p", tag[1] as string]);
		if (counterparts.length === 0) {
			throw new Error(`direct conversation ${request.channelID} names nobody else to open it with`);
		}
		const acknowledgement = await relay.publishForAcknowledgement(DM_OPEN_KIND, "", counterparts);
		const openedChannelID = channelIDFromAcknowledgement(acknowledgement);
		if (openedChannelID !== request.channelID) {
			throw new Error(
				`opening direct conversation ${request.channelID} again answered ${openedChannelID ?? "no conversation"}`,
			);
		}
	});
}
