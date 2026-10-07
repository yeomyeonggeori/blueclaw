import type { UserMessage } from "../adapters/buzz/user-session.ts";
import type { PersonalMessage } from "./gateway.ts";

export function personalMessageOf(message: UserMessage): PersonalMessage {
	return {
		id: message.id,
		conversationID: message.conversationID,
		parentID: message.parentID,
		authorExternalID: message.authorPubkeyHex,
		body: message.body,
		postedAt: message.postedAt,
		mentions: { externalIDs: message.mentions.pubkeyHexes, isEveryone: message.mentions.isEveryone },
		reactions: message.reactions.map((reaction) => ({
			emoji: reaction.emoji,
			imageURL: reaction.imageURL,
			byExternalIDs: reaction.byPubkeyHexes,
		})),
		attachments: message.attachments.map((attachment) => ({
			id: attachment.url,
			filename: attachment.filename,
			contentType: attachment.contentType,
			sizeBytes: attachment.sizeBytes,
			digest: attachment.digest,
		})),
	};
}
