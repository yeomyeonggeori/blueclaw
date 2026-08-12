import type { BuzzAdapter } from "../adapters/buzz/adapter.ts";
import {
	addReactionAsUser,
	deleteChannelMessageAsUser,
	editChannelMessageAsUser,
	ensureUserDirectMessageChannel,
	listUserConversations,
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
		return { externalID, name: user?.fullName };
	}

	async listConversations(actor: ActorCredential): Promise<PersonalConversation[]> {
		this.require(actor);
		const conversations = await listUserConversations(this.settings.relayURL, actor.secret);
		return conversations.map((conversation) => ({
			id: conversation.channelID,
			name: conversation.name,
			kind: conversation.isDM ? "dm" : "group",
			avatarURL: conversation.avatarURL,
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

	async listMessages(actor: ActorCredential): Promise<PersonalMessagePage> {
		this.require(actor);
		throw new UnsupportedByPlatform(this.platform, "read messages");
	}

	async sendMessage(
		actor: ActorCredential,
		conversationID: string,
		body: string,
		parentID?: string,
	): Promise<PersonalMessage> {
		this.require(actor);
		const messageID = await sendChannelMessageAsUser({
			relayURL: this.settings.relayURL,
			userSecretHex: actor.secret,
			channelID: conversationID,
			message: body,
			replyToRootId: parentID,
			authTagJSON: this.settings.authTagJSON,
		});
		return {
			id: messageID,
			conversationID,
			parentID,
			authorExternalID: pubkeyFromSecret(actor.secret),
			body,
			postedAt: new Date().toISOString(),
			reactions: [],
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

	async readProfilePicture(actor: ActorCredential): Promise<PersonalImage | null> {
		this.require(actor);
		return null;
	}

	private require(actor: ActorCredential): void {
		requireMatchingCredential(this, actor);
	}
}
