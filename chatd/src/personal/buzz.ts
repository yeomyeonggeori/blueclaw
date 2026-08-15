import type { BuzzAdapter } from "../adapters/buzz/adapter.ts";
import type { OutgoingAttachment } from "../outgoing-attachment.ts";
import {
	addReactionAsUser,
	deleteChannelMessageAsUser,
	editChannelMessageAsUser,
	ensureUserDirectMessageChannel,
	listChannelMessagesAsUser,
	listUserConversations,
	profilePictureURLAsUser,
	pubkeyFromSecret,
	sendChannelMessageAsUser,
} from "../adapters/buzz/user-session.ts";
import {
	CredentialRefused,
	UnsupportedByPlatform,
	requireMatchingCredential,
	type ActorCredential,
	type CredentialRequirement,
	type PersonalConversation,
	type PersonalEmoji,
	type PersonalGateway,
	type IssuedCredential,
	type PersonalIdentity,
	type PersonalFile,
	type PersonalImage,
	type PersonalMessage,
	type PersonalMessagePage,
	type PersonalPerson,
} from "./gateway.ts";

export type BuzzPersonalSettings = {
	relayURL: string;
	authTagJSON?: string;
};

export function createBuzzPersonalGateway(
	adapter: BuzzAdapter,
	settings: BuzzPersonalSettings,
): PersonalGateway {
	return new BuzzPersonalGateway(adapter, settings);
}

class BuzzPersonalGateway implements PersonalGateway {
	readonly platform = "buzz";
	readonly credentialKind = "buzz-secret";

	constructor(
		private readonly adapter: BuzzAdapter,
		private readonly settings: BuzzPersonalSettings,
	) {}

	credentialRequirement(): CredentialRequirement {
		return {
			kind: "secret",
			fields: [{ name: "secret", label: "Your Buzz secret key", isSecret: true }],
		};
	}

	async issueCredential(answers: Record<string, string>): Promise<IssuedCredential> {
		const secret = answers.secret ?? "";
		if (!secret) throw new CredentialRefused("a Buzz identity needs its secret key");
		const credential = { kind: this.credentialKind, secret };
		return { credential, identity: await this.identity(credential) };
	}

	async identity(actor: ActorCredential): Promise<PersonalIdentity> {
		this.require(actor);
		const externalID = pubkeyFromSecret(actor.secret);
		const user = await this.adapter.getUser(externalID).catch(() => null);
		return { externalID, name: user?.fullName, serverURL: this.settings.relayURL };
	}

	async listConversations(actor: ActorCredential): Promise<PersonalConversation[]> {
		this.require(actor);
		const conversations = await listUserConversations(this.settings.relayURL, actor.secret);
		return conversations.map((conversation) => ({
			id: conversation.channelID,
			name: conversation.name,
			kind: conversation.isDM ? "dm" : "group",
			participantExternalIDs: conversation.participantPubkeyHexes,
		}));
	}

	async listPeople(actor: ActorCredential): Promise<PersonalPerson[]> {
		this.require(actor);
		const people = await this.adapter.listPeople(pubkeyFromSecret(actor.secret));
		return people.map((person) => ({
			externalID: person.id,
			name: person.name,
			avatarURL: person.avatarURL,
		}));
	}

	async ensureDirectConversation(
		actor: ActorCredential,
		counterpartExternalIDs: string[],
	): Promise<PersonalConversation> {
		this.require(actor);
		const counterpart = counterpartExternalIDs[0] ?? this.adapter.botPubkey;
		if (counterpartExternalIDs.length > 1) {
			throw new UnsupportedByPlatform(this.platform, "open a group direct conversation");
		}
		const channel = await ensureUserDirectMessageChannel(
			this.settings.relayURL,
			actor.secret,
			counterpart,
		);
		return { id: channel.channelID, name: "", kind: "dm" };
	}

	async listMessages(
		actor: ActorCredential,
		conversationID: string,
		before?: string,
	): Promise<PersonalMessagePage> {
		this.require(actor);
		const wanted = 50;
		const read = await listChannelMessagesAsUser({
			relayURL: this.settings.relayURL,
			userSecretHex: actor.secret,
			channelID: conversationID,
			limit: wanted,
			before,
			authTagJSON: this.settings.authTagJSON,
		});
		return {
			messages: read.map((message) => ({
				id: message.id,
				conversationID: message.conversationID,
				parentID: message.parentID,
				authorExternalID: message.authorPubkeyHex,
				body: message.body,
				postedAt: message.postedAt,
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
				})),
			})),
			hasMoreBefore: read.length >= wanted,
		};
	}

	async sendMessage(
		actor: ActorCredential,
		conversationID: string,
		body: string,
		parentID?: string,
		attachments: OutgoingAttachment[] = [],
	): Promise<PersonalMessage> {
		this.require(actor);
		const sent = await sendChannelMessageAsUser({
			relayURL: this.settings.relayURL,
			userSecretHex: actor.secret,
			channelID: conversationID,
			message: body,
			attachments,
			replyToRootId: parentID,
			authTagJSON: this.settings.authTagJSON,
		});
		return {
			id: sent.id,
			conversationID,
			parentID,
			authorExternalID: pubkeyFromSecret(actor.secret),
			body: sent.body,
			postedAt: new Date().toISOString(),
			reactions: [],
			attachments: sent.attachments.map((attachment) => ({
				id: attachment.url,
				filename: attachment.filename,
				contentType: attachment.contentType,
				sizeBytes: attachment.sizeBytes,
			})),
		};
	}

	async editMessage(
		actor: ActorCredential,
		conversationID: string,
		messageID: string,
		body: string,
	): Promise<PersonalMessage> {
		this.require(actor);
		const editID = await editChannelMessageAsUser({
			relayURL: this.settings.relayURL,
			userSecretHex: actor.secret,
			channelID: conversationID,
			targetEventId: messageID,
			message: body,
			authTagJSON: this.settings.authTagJSON,
		});
		return {
			id: editID,
			conversationID,
			authorExternalID: pubkeyFromSecret(actor.secret),
			body,
			postedAt: new Date().toISOString(),
			editedAt: new Date().toISOString(),
			reactions: [],
			attachments: [],
		};
	}

	async deleteMessage(
		actor: ActorCredential,
		conversationID: string,
		messageID: string,
	): Promise<void> {
		this.require(actor);
		await deleteChannelMessageAsUser({
			relayURL: this.settings.relayURL,
			userSecretHex: actor.secret,
			channelID: conversationID,
			targetEventId: messageID,
			authTagJSON: this.settings.authTagJSON,
		});
	}

	async addReaction(
		actor: ActorCredential,
		conversationID: string,
		messageID: string,
		emoji: string,
	): Promise<void> {
		this.require(actor);
		await addReactionAsUser({
			relayURL: this.settings.relayURL,
			userSecretHex: actor.secret,
			channelID: conversationID,
			targetEventId: messageID,
			emoji,
			authTagJSON: this.settings.authTagJSON,
		});
	}

	async removeReaction(actor: ActorCredential): Promise<void> {
		this.require(actor);
		throw new UnsupportedByPlatform(this.platform, "take a reaction back");
	}

	async listCustomEmoji(actor: ActorCredential): Promise<PersonalEmoji[]> {
		this.require(actor);
		return [];
	}

	async readCustomEmojiImage(actor: ActorCredential): Promise<PersonalImage | null> {
		this.require(actor);
		return null;
	}

	// A buzz profile names a picture by url rather than carrying it, and the
	// caller wants the bytes.
	async readProfilePicture(
		actor: ActorCredential,
		externalID: string,
		largestBytes: number,
	): Promise<PersonalImage | null> {
		this.require(actor);
		const pictureURL = await profilePictureURLAsUser(this.settings.relayURL, actor.secret, externalID);
		if (!pictureURL) return null;
		const held = await readWithinLimit(pictureURL, largestBytes, "image/png");
		if (!held) return null;
		return { dataURL: `data:${held.contentType};base64,${held.contentBase64}` };
	}

	// An attachment's id is the url the message named it by, so reading one is
	// fetching what the message already points at rather than looking it up.
	async readAttachment(
		actor: ActorCredential,
		attachmentID: string,
		largestBytes: number,
	): Promise<PersonalFile | null> {
		this.require(actor);
		if (!attachmentID.startsWith(this.mediaBaseURL())) return null;
		const held = await readWithinLimit(attachmentID, largestBytes, "application/octet-stream");
		if (!held) return null;
		return {
			filename: attachmentID.slice(attachmentID.lastIndexOf("/") + 1),
			contentType: held.contentType,
			contentBase64: held.contentBase64,
		};
	}

	private mediaBaseURL(): string {
		return this.settings.relayURL.replace(/^ws/, "http").replace(/\/+$/, "");
	}

	private require(actor: ActorCredential): void {
		requireMatchingCredential(this, actor);
	}
}

// A file too big for the caller is not an error to raise, it is a file they do
// not get; the same is true of one the relay will not serve.
async function readWithinLimit(
	url: string,
	largestBytes: number,
	fallbackContentType: string,
): Promise<{ contentType: string; contentBase64: string } | null> {
	const response = await fetch(url);
	if (!response.ok) return null;
	const bytes = new Uint8Array(await response.arrayBuffer());
	if (bytes.byteLength > largestBytes) return null;
	return {
		contentType: response.headers.get("content-type") ?? fallbackContentType,
		contentBase64: Buffer.from(bytes).toString("base64"),
	};
}
