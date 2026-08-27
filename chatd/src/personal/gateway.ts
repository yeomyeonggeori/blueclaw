import type { OutgoingAttachment } from "../outgoing-attachment.ts";

export type ActorCredential = {
	kind: string;
	secret: string;
};

// A person signing into the messenger app itself needs the address to sign in
// at, which the company's server knows and the browser does not.
export type PersonalIdentity = {
	externalID: string;
	name?: string;
	serverURL?: string;
};

export type PersonalConversation = {
	id: string;
	name: string;
	kind: "dm" | "group";
	avatarURL?: string;
	participantExternalIDs?: string[];
	webURL?: string;
};

export type PersonalPerson = {
	externalID: string;
	name: string;
	avatarURL?: string;
};

// A platform that keeps its custom emoji in a registry leaves the picture out
// and answers for the name separately; one that puts it on the reaction says so
// here.
export type PersonalReaction = {
	emoji: string;
	imageURL?: string;
	byExternalIDs: string[];
};

// A store that addresses a file by the hash of its contents says so here, so a
// reader that keeps its own copy can tell it already has these bytes without
// fetching them again. Empty when the platform names files some other way.
export type PersonalAttachment = {
	id: string;
	filename: string;
	contentType: string;
	sizeBytes: number;
	digest: string;
	widthPixels?: number;
	heightPixels?: number;
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
	attachments: PersonalAttachment[];
};

export type PersonalMessagePage = {
	messages: PersonalMessage[];
	hasMoreBefore: boolean;
};

export type CredentialField = {
	name: string;
	label: string;
	isSecret: boolean;
};

export type CredentialRequirement = {
	kind: "sign-in" | "secret" | "redirect";
	credentialKind: string;
	fields: CredentialField[];
	redirectURL?: string;
};

export type IssuedCredential = {
	credential: ActorCredential;
	identity: PersonalIdentity;
};

export class CredentialRefused extends Error {
	constructor(message: string) {
		super(message);
		this.name = "CredentialRefused";
	}
}

export type PersonalEmoji = {
	name: string;
};

export type PersonalImage = {
	dataURL: string;
};

export type PersonalFile = {
	filename: string;
	contentType: string;
	contentBase64: string;
};

export interface PersonalGateway {
	readonly platform: string;
	readonly credentialKind: string;

	credentialRequirement(): CredentialRequirement;
	issueCredential(answers: Record<string, string>): Promise<IssuedCredential>;

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
		attachments?: OutgoingAttachment[],
	): Promise<PersonalMessage>;
	readAttachment(
		actor: ActorCredential,
		attachmentID: string,
		largestBytes: number,
	): Promise<PersonalFile | null>;
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
	listCustomEmoji(actor: ActorCredential): Promise<PersonalEmoji[]>;
	readCustomEmojiImage(
		actor: ActorCredential,
		name: string,
		largestBytes: number,
	): Promise<PersonalImage | null>;
	readProfilePicture(
		actor: ActorCredential,
		externalID: string,
		largestBytes: number,
	): Promise<PersonalImage | null>;
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
