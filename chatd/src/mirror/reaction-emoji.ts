import { defaultEmojiResolver } from "chat";

// A platform names a reaction and Buzz carries the character, so the mirror
// speaks one of the two and each adapter converts at its own edge. Naming it
// here rather than inside an adapter keeps the echo suppressor comparing like
// with like: what a platform reports and what was published have to be the
// same string or a reaction the mirror caused reads as a new one.
const additionalReactionEmojiCharacters: Record<string, string> = {
	clap: "\u{1F44F}",
	mag: "\u{1F50D}",
	sweat_smile: "\u{1F605}",
	wave: "\u{1F44B}",
	hourglass_flowing_sand: "\u{23F3}",
};

export function reactionContentOf(emojiName: string): string {
	const additionalCharacter = additionalReactionEmojiCharacters[emojiName];
	if (additionalCharacter) return additionalCharacter;
	return defaultEmojiResolver.toGChat(defaultEmojiResolver.fromSlack(emojiName));
}
