export type ActorCredential = {
	kind: string;
	secret: string;
};

export type PersonalIdentity = {
	externalID: string;
	name?: string;
};

export type PersonalConversation = {
	id: string;
	name: string;
	kind: "dm" | "group";
	avatarURL?: string;
	participantExternalIDs?: string[];
};

export type PersonalPerson = {
	externalID: string;
	name: string;
	avatarURL?: string;
};

export type PersonalReaction = {
	emoji: string;
	byExternalIDs: string[];
};

export type PersonalMessage = {
	id: string;
	conversationID: string;
	parentID?: string;
	authorExternalID: string;
	body: string;
	postedAt: string;
	editedAt?: string;
	reactions: PersonalReaction[];
};

export type PersonalMessagePage = {
	messages: PersonalMessage[];
	hasMoreBefore: boolean;
};

export interface PersonalGateway {
	readonly platform: string;
	readonly credentialKind: string;

	identity(actor: ActorCredential): Promise<PersonalIdentity>;
	listConversations(actor: ActorCredential): Promise<PersonalConversation[]>;
	listPeople(actor: ActorCredential): Promise<PersonalPerson[]>;
	ensureDirectConversation(
		actor: ActorCredential,
		counterpartExternalIDs: string[],
	): Promise<PersonalConversation>;
	listMessages(
		actor: ActorCredential,
		conversationID: string,
		before?: string,
	): Promise<PersonalMessagePage>;
	sendMessage(
		actor: ActorCredential,
		conversationID: string,
		body: string,
		parentID?: string,
	): Promise<PersonalMessage>;
	editMessage(
		actor: ActorCredential,
		conversationID: string,
		messageID: string,
		body: string,
	): Promise<PersonalMessage>;
	deleteMessage(actor: ActorCredential, conversationID: string, messageID: string): Promise<void>;
	addReaction(
		actor: ActorCredential,
		conversationID: string,
		messageID: string,
		emoji: string,
	): Promise<void>;
	removeReaction(
		actor: ActorCredential,
		conversationID: string,
		messageID: string,
		emoji: string,
	): Promise<void>;
}

export class UnsupportedByPlatform extends Error {
	constructor(platform: string, operation: string) {
		super(`${platform} cannot ${operation} as a person`);
		this.name = "UnsupportedByPlatform";
	}
}

export function requireMatchingCredential(gateway: PersonalGateway, actor: ActorCredential): void {
	if (actor.kind === gateway.credentialKind) return;
	throw new Error(
		`${gateway.platform} needs a ${gateway.credentialKind} credential, not ${actor.kind}`,
	);
}
