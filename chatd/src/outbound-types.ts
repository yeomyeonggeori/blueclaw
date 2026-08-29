import type { VisibleContextMessageDocument } from "./visible-context.ts";

export interface ReplyAttachmentDocument {
	devicePath?: string;
	filename?: string;
	contentType?: string;
	sizeBytes?: number;
	title?: string;
	contentBase64?: string;
}

export interface AskChoiceOptionDocument {
	key: string;
	label: string;
	shortLabel?: string;
	value?: string;
}

export interface AskInteractionDocument {
	interactionID?: string;
	taskRunID?: string;
	kind?: string;
	message?: string;
	question?: string;
	options?: AskChoiceOptionDocument[];
	recommendedOptionKey?: string;
	selectionMode?: string;
	responseLanguage?: string;
	targetPlatformUserID?: string;
}

export interface ReplySendRequest {
	replyTargetID: string;
	// The message this reply answers, so a reply written deep in a thread is
	// answered where it was written rather than at the top of the thread.
	answeringMessageID?: string;
	message: string;
	taskRunID?: string;
	replyKind?: string;
	rawEventID?: string;
	outboxID?: string;
	attachments?: ReplyAttachmentDocument[];
	interaction?: AskInteractionDocument;
	isError?: boolean;
}

export interface ReplySendResponse {
	dispatchID: string;
}

export interface ProgressRequest {
	replyTargetID: string;
}

export interface ReactionRequest {
	conversationID?: string;
	messageID: string;
	emojiName: string;
	reason?: string;
}

export interface HistoryFetchRequest {
	historyCursor?: string;
	threadID?: string;
	channelID?: string;
	channelName?: string;
	limit?: number;
	direction?: string;
}

export interface HistoryFetchResponse {
	messages: VisibleContextMessageDocument[];
	hasMoreBefore: boolean;
	historyCursor: string;
	channelID?: string;
	channelName?: string;
	conversationType?: string;
}

export interface ChannelEnsureRequest {
	name: string;
	displayName?: string;
	description?: string;
	topic?: string;
}

export interface ChannelEnsureResponse {
	channelID: string;
	replyTargetID: string;
	created: boolean;
}

export interface DirectMessageSendRequest {
	userSecretHex: string;
	message: string;
	attachments?: ReplyAttachmentDocument[];
	channelId?: string;
	replyToRootId?: string;
}

export interface DirectMessagePostRequest {
	counterpartPubkeyHex: string;
	message: string;
}

export interface DirectMessagePostResponse {
	channelID: string;
	messageID: string;
}

export interface ConversationsListRequest {
	userSecretHex: string;
}

export interface ConversationDocument {
	id: string;
	name: string;
	kind: "dm" | "group";
	avatarURL?: string;
}

export interface ConversationsListResponse {
	conversations: ConversationDocument[];
}

export interface DirectMessageSendResponse {
	channelID: string;
	replyTargetID: string;
	messageID: string;
}

export interface DirectMessageEnsureRequest {
	userSecretHex: string;
	channelId?: string;
	counterpartPubkeyHex?: string;
}

export interface PeopleListResponse {
	people: { id: string; name: string; avatarURL?: string }[];
}

export interface DirectMessageEnsureResponse {
	channelID: string;
	replyTargetID: string;
	historyCursor: string;
	userPubkeyHex: string;
	botName?: string;
	botAvatarURL?: string;
}

export interface MessageEditRequest {
	replyTargetID: string;
	messageID: string;
	message: string;
	requesterPubkeyHex?: string;
	attachments?: ReplyAttachmentDocument[];
}

export interface MessageDeleteRequest {
	replyTargetID: string;
	messageID: string;
	requesterPubkeyHex?: string;
}

export interface MessageSearchRequest {
	replyTargetID?: string;
	channelID?: string;
	channelName?: string;
	rootMessageID?: string;
	messageIDs?: string[];
	authoredBy?: string;
	requesterPubkeyHex?: string;
	queries?: string[];
	limit?: number;
}

export interface MessageSearchCandidateDocument {
	messageID: string;
	channelID: string;
	rootMessageID?: string;
	authorPubkeyHex: string;
	authoredByAssistant: boolean;
	editable: boolean;
	deletable: boolean;
	createdAt: number;
	text: string;
	score: number;
}

export interface MessageSearchResponse {
	channelID: string;
	candidates: MessageSearchCandidateDocument[];
}

export interface InputAttachmentDocument {
	platform?: string;
	fileID?: string;
	url?: string;
	messageID?: string;
	filename?: string;
	contentType?: string;
	sizeBytes?: number;
	path?: string;
	contentBase64?: string;
	isAvailable?: boolean;
	errorCode?: string;
	message?: string;
}

export interface AttachmentImportRequest {
	messageID: string;
	targetDirectoryPath: string;
	inputAttachments: InputAttachmentDocument[];
}

export interface AgentPartSourceDocument {
	platform?: string;
	messageID?: string;
	fileID?: string;
}

export interface AgentFilePartDocument {
	path: string;
	filename?: string;
	contentType?: string;
	sizeBytes?: number;
}

export interface AgentImagePartDocument {
	path: string;
	filename?: string;
	mimeType?: string;
}

export interface AgentPartDocument {
	type: "file" | "image";
	file?: AgentFilePartDocument;
	image?: AgentImagePartDocument;
	source?: AgentPartSourceDocument;
}

export interface AttachmentImportResponse {
	inputParts: AgentPartDocument[];
	inputAttachments: InputAttachmentDocument[];
}

export interface MessagePostRequest {
	threadID?: string;
	channelID?: string;
	channelName?: string;
	message: string;
	attachments?: ReplyAttachmentDocument[];
}

export interface MessagePostResponse {
	messageID: string;
	channelID?: string;
}

export interface IdentityResolveRequest {
	senderID: string;
}

export interface IdentitySelfResponse {
	pubkeyHex: string;
	name: string;
}

export interface IdentityResolveResponse {
	displayName?: string;
	email?: string;
}
