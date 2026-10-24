import type { QueryingRelay } from "./deletions.ts";
import { withRelayAs } from "./relay-pool.ts";
import type { BuzzEvent } from "./types.ts";

// NIP-30 custom emoji, as Buzz keeps them: each member publishes their own
// kind 30030 set under one d tag, and the community's emoji is the union of
// every set (block/buzz desktop/src/shared/api/customEmoji.ts).
const EMOJI_SET_KIND = 30030;
const CUSTOM_EMOJI_SET_D_TAG = "buzz:custom-emoji";
const SHORTCODE = /^[a-z0-9_-]{1,64}$/;
const SHORTCODE_IN_TEXT = /:([a-z0-9_-]+):/gi;

export type CustomEmoji = { shortcode: string; url: string };

export async function listCustomEmojiAsUser(
	relayURL: string,
	userSecretHex: string,
	authTagJSON: string | undefined,
): Promise<CustomEmoji[]> {
	return withRelayAs(relayURL, userSecretHex, authTagJSON, communityEmojiOf);
}

export async function communityEmojiOf(relay: QueryingRelay): Promise<CustomEmoji[]> {
	return unionOfEmojiSets(await relay.query({ kinds: [EMOJI_SET_KIND], "#d": [CUSTOM_EMOJI_SET_D_TAG] }));
}

// Members who disagree on a shortcode's picture are settled the way Buzz
// settles them: the latest set wins, and a tie goes to the smaller URL.
export function unionOfEmojiSets(sets: BuzzEvent[]): CustomEmoji[] {
	const chosen = new Map<string, { url: string; createdAt: number }>();
	for (const set of sets) {
		for (const emoji of emojiTaggedOn(set)) {
			const known = chosen.get(emoji.shortcode);
			const isKnownNewer = known && (known.createdAt > set.created_at || (known.createdAt === set.created_at && known.url <= emoji.url));
			if (!isKnownNewer) chosen.set(emoji.shortcode, { url: emoji.url, createdAt: set.created_at });
		}
	}
	return [...chosen]
		.map(([shortcode, { url }]) => ({ shortcode, url }))
		.sort((first, second) => first.shortcode.localeCompare(second.shortcode));
}

export function emojiTaggedOn(event: BuzzEvent): CustomEmoji[] {
	const seen = new Set<string>();
	const emoji: CustomEmoji[] = [];
	for (const [name, rawShortcode, url] of event.tags) {
		if (name !== "emoji" || !rawShortcode || !url) continue;
		const shortcode = normalizedShortcode(rawShortcode);
		if (!shortcode || seen.has(shortcode)) continue;
		seen.add(shortcode);
		emoji.push({ shortcode, url });
	}
	return emoji;
}

export function normalizedShortcode(raw: string): string | undefined {
	const shortcode = raw.trim().replace(/^:+|:+$/g, "").toLowerCase();
	return SHORTCODE.test(shortcode) ? shortcode : undefined;
}

export function shortcodesIn(text: string): string[] {
	return [...text.matchAll(SHORTCODE_IN_TEXT)].map(([, shortcode]) => (shortcode ?? "").toLowerCase());
}

export function emojiTagsFor(text: string, community: CustomEmoji[]): string[][] {
	const urlByShortcode = new Map(community.map((emoji) => [emoji.shortcode, emoji.url]));
	return [...new Set(shortcodesIn(text))].flatMap((shortcode) => {
		const url = urlByShortcode.get(shortcode);
		return url ? [["emoji", shortcode, url]] : [];
	});
}
