import type { BuzzEvent } from "./types.ts";

export type UserMentions = {
	pubkeyHexes: string[];
	isEveryone: boolean;
};

const EVERYONE_TAG = "mention";
const EVERYONE_VALUE = "everyone";

export function mentionTags(mentions: UserMentions | undefined): string[][] {
	if (!mentions) return [];
	const tags = distinct(mentions.pubkeyHexes).map((pubkeyHex) => ["p", pubkeyHex]);
	if (mentions.isEveryone) tags.push([EVERYONE_TAG, EVERYONE_VALUE]);
	return tags;
}

export function mentionsOf(event: BuzzEvent): UserMentions {
	const pubkeyHexes: string[] = [];
	let isEveryone = false;
	for (const tag of event.tags) {
		if (tag[0] === "p" && typeof tag[1] === "string" && tag[1].trim() !== "") pubkeyHexes.push(tag[1]);
		if (tag[0] === EVERYONE_TAG && tag[1] === EVERYONE_VALUE) isEveryone = true;
	}
	return { pubkeyHexes: distinct(pubkeyHexes), isEveryone };
}

function distinct(pubkeyHexes: string[]): string[] {
	return [...new Set(pubkeyHexes.filter((pubkeyHex) => pubkeyHex.trim() !== ""))];
}
