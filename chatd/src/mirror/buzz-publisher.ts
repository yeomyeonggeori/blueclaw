import { reactionContentOf } from '../adapters/buzz/adapter.ts';
import {
	addReactionAsUser,
	deleteChannelMessageAsUser,
	editChannelMessageAsUser,
	sendChannelMessageAsUser,
} from '../adapters/buzz/user-session.ts';
import type { BuzzDelete, BuzzEdit, BuzzGateway, BuzzPublish, BuzzReaction } from './orchestrator.ts';
import { originTag } from './origin.ts';

type BuzzUserSession = {
	send: typeof sendChannelMessageAsUser;
	edit: typeof editChannelMessageAsUser;
	remove: typeof deleteChannelMessageAsUser;
	react: typeof addReactionAsUser;
};

const liveSession: BuzzUserSession = {
	send: sendChannelMessageAsUser,
	edit: editChannelMessageAsUser,
	remove: deleteChannelMessageAsUser,
	react: addReactionAsUser,
};

// Mirrors messages to Buzz signed by the originating person's own derived key
// (per-user puppeting, never a bot relay). Every write carries an origin tag so
// the fan-out never sends the event back to the platform it came from.
export function createBuzzGateway(
	relayURL: string,
	authTagJSON: string | undefined,
	session: BuzzUserSession = liveSession,
): BuzzGateway {
	return {
		async publish(publish: BuzzPublish): Promise<{ eventId: string }> {
			const sent = await session.send({
				relayURL,
				authTagJSON,
				userSecretHex: publish.userSecretHex,
				channelID: publish.buzzChannelId,
				message: publish.text,
				replyToRootId: publish.replyToBuzzEventId,
				extraTags: [originTag(publish.origin)],
			});
			return { eventId: sent.id };
		},
		async edit(edit: BuzzEdit): Promise<void> {
			await session.edit({
				relayURL,
				authTagJSON,
				userSecretHex: edit.userSecretHex,
				channelID: edit.buzzChannelId,
				targetEventId: edit.targetEventId,
				message: edit.text,
				extraTags: [originTag(edit.origin)],
			});
		},
		async remove(remove: BuzzDelete): Promise<void> {
			await session.remove({
				relayURL,
				authTagJSON,
				userSecretHex: remove.userSecretHex,
				channelID: remove.buzzChannelId,
				targetEventId: remove.targetEventId,
				extraTags: [originTag(remove.origin)],
			});
		},
		// react.emoji arrives as the platform's name for it, not a character.
		async react(react: BuzzReaction): Promise<void> {
			await session.react({
				relayURL,
				authTagJSON,
				userSecretHex: react.userSecretHex,
				channelID: react.buzzChannelId,
				targetEventId: react.targetEventId,
				emoji: reactionContentOf(react.emoji),
				extraTags: [originTag(react.origin)],
			});
		},
	};
}
