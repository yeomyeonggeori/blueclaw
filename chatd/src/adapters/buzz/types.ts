export const BUZZ_ADAPTER_NAME = "buzz";

export type BuzzThreadId = {
	channelId: string;
	rootEventId?: string;
};

export type BuzzEvent = {
	id: string;
	pubkey: string;
	created_at: number;
	kind: number;
	tags: string[][];
	content: string;
	sig: string;
};

export type BuzzChannel = {
	channelId: string;
	name: string;
	isDM: boolean;
};

export type BuzzAdapterConfig = {
	relayURL: string;
	privateKeyHex: string;
	botDisplayName: string;
	accountLinksPath?: string;
	authTagJSON?: string;
	mirror?: import("../../mirror/inbound.ts").BuzzMirrorSink;
};

export function firstTagValue(event: BuzzEvent, tagName: string): string | undefined {
	return event.tags.find((tag) => tag[0] === tagName)?.[1];
}

export function carriesTag(event: BuzzEvent, tagName: string): boolean {
	return event.tags.some((tag) => tag[0] === tagName);
}

export function threadTagsOf(event: BuzzEvent): { rootEventId?: string; parentEventId?: string } {
	let rootEventId: string | undefined;
	let parentEventId: string | undefined;
	let firstEventId: string | undefined;
	for (const tag of event.tags) {
		if (tag[0] !== "e" || !tag[1]) continue;
		firstEventId = firstEventId ?? tag[1];
		if (tag[3] === "root") rootEventId = tag[1];
		else if (tag[3] === "reply") parentEventId = tag[1];
	}
	rootEventId = rootEventId ?? parentEventId ?? firstEventId;
	return { rootEventId, parentEventId: parentEventId ?? rootEventId };
}
