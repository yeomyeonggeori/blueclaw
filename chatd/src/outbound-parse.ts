import type {
	AskChoiceOptionDocument,
	AskInteractionDocument,
	AttachmentImportRequest,
	ChannelEnsureRequest,
	DirectMessageEnsureRequest,
	DirectMessageSendRequest,
	HistoryFetchRequest,
	IdentityResolveRequest,
	InputAttachmentDocument,
	MessageDeleteRequest,
	MessageEditRequest,
	ProgressRequest,
	ReactionRequest,
	ReplyAttachmentDocument,
	ReplySendRequest,
} from "./outbound-types.ts";

function requireRecord(value: unknown, context: string): Record<string, unknown> {
	if (typeof value !== "object" || value === null) {
		throw new Error(`expected ${context} to be a JSON object`);
	}
	return value as Record<string, unknown>;
}

function requireString(record: Record<string, unknown>, field: string): string {
	const value = record[field];
	if (typeof value !== "string" || value.trim().length === 0) {
		throw new Error(`missing required field ${field}`);
	}
	return value;
}

function optionalString(record: Record<string, unknown>, field: string): string | undefined {
	const value = record[field];
	return typeof value === "string" ? value : undefined;
}

function optionalNumber(record: Record<string, unknown>, field: string): number | undefined {
	const value = record[field];
	return typeof value === "number" ? value : undefined;
}

function optionalArray(record: Record<string, unknown>, field: string): unknown[] {
	const value = record[field];
	return Array.isArray(value) ? value : [];
}

function parseReplyAttachment(value: unknown): ReplyAttachmentDocument {
	const record = requireRecord(value, "reply attachment");
	return {
		devicePath: optionalString(record, "devicePath"),
		filename: optionalString(record, "filename"),
		contentType: optionalString(record, "contentType"),
		sizeBytes: optionalNumber(record, "sizeBytes"),
		title: optionalString(record, "title"),
		contentBase64: optionalString(record, "contentBase64"),
	};
}

function parseAskChoiceOption(value: unknown): AskChoiceOptionDocument {
	const record = requireRecord(value, "ask choice option");
	return {
		key: requireString(record, "key"),
		label: requireString(record, "label"),
		shortLabel: optionalString(record, "shortLabel"),
		value: optionalString(record, "value"),
	};
}

function parseAskInteraction(value: unknown): AskInteractionDocument {
	const record = requireRecord(value, "interaction");
	return {
		interactionID: optionalString(record, "interactionID"),
		taskRunID: optionalString(record, "taskRunID"),
		kind: optionalString(record, "kind"),
		message: optionalString(record, "message"),
		question: optionalString(record, "question"),
		options: optionalArray(record, "options").map(parseAskChoiceOption),
		recommendedOptionKey: optionalString(record, "recommendedOptionKey"),
		selectionMode: optionalString(record, "selectionMode"),
		responseLanguage: optionalString(record, "responseLanguage"),
		targetPlatformUserID: optionalString(record, "targetPlatformUserID"),
	};
}

export function parseReplySendRequest(value: unknown): ReplySendRequest {
	const record = requireRecord(value, "reply.send request");
	const interactionValue = record.interaction;
	return {
		replyTargetID: requireString(record, "replyTargetID"),
		answeringMessageID: optionalString(record, "answeringMessageID"),
		message: requireString(record, "message"),
		taskRunID: optionalString(record, "taskRunID"),
		replyKind: optionalString(record, "replyKind"),
		rawEventID: optionalString(record, "rawEventID"),
		outboxID: optionalString(record, "outboxID"),
		attachments: optionalArray(record, "attachments").map(parseReplyAttachment),
		interaction: interactionValue === undefined ? undefined : parseAskInteraction(interactionValue),
		isError: hasFailureNotice(record.failureNotice),
	};
}

function hasFailureNotice(value: unknown): boolean {
	if (typeof value !== "object" || value === null) return false;
	const message = (value as Record<string, unknown>).message;
	return typeof message === "string" && message.trim().length > 0;
}

export function parseProgressRequest(value: unknown): ProgressRequest {
	const record = requireRecord(value, "progress request");
	return { replyTargetID: requireString(record, "replyTargetID") };
}

export function parseReactionRequest(value: unknown): ReactionRequest {
	const record = requireRecord(value, "reaction request");
	return {
		conversationID: optionalString(record, "conversationID"),
		messageID: requireString(record, "messageID"),
		emojiName: requireString(record, "emojiName"),
		reason: optionalString(record, "reason"),
	};
}

export function parseHistoryFetchRequest(value: unknown): HistoryFetchRequest {
	const record = requireRecord(value, "history.fetch request");
	return {
		historyCursor: requireString(record, "historyCursor"),
		limit: optionalNumber(record, "limit"),
		direction: optionalString(record, "direction"),
	};
}

export function parseMessageEditRequest(value: unknown): MessageEditRequest {
	const record = requireRecord(value, "message.edit request");
	return {
		replyTargetID: requireString(record, "replyTargetID"),
		messageID: requireString(record, "messageID"),
		message: requireString(record, "message"),
	};
}

export function parseMessageDeleteRequest(value: unknown): MessageDeleteRequest {
	const record = requireRecord(value, "message_delete request");
	return {
		replyTargetID: requireString(record, "replyTargetID"),
		messageID: requireString(record, "messageID"),
	};
}

export function parseDirectMessageSendRequest(value: unknown): DirectMessageSendRequest {
	const record = requireRecord(value, "dm.send request");
	return {
		userSecretHex: requireString(record, "userSecretHex"),
		message: optionalString(record, "message") ?? "",
		attachments: optionalArray(record, "attachments").map(parseReplyAttachment),
		channelId: optionalString(record, "channelId"),
		replyToRootId: optionalString(record, "replyToRootId"),
	};
}

export function parseDirectMessageEnsureRequest(value: unknown): DirectMessageEnsureRequest {
	const record = requireRecord(value, "dm.ensure request");
	return {
		userSecretHex: requireString(record, "userSecretHex"),
		channelId: optionalString(record, "channelId"),
		counterpartPubkeyHex: optionalString(record, "counterpartPubkeyHex"),
	};
}

export function parseConversationsListRequest(value: unknown): { userSecretHex: string } {
	const record = requireRecord(value, "conversations.list request");
	return { userSecretHex: requireString(record, "userSecretHex") };
}

export function parsePeopleListRequest(value: unknown): { userSecretHex: string } {
	const record = requireRecord(value, "people.list request");
	return { userSecretHex: requireString(record, "userSecretHex") };
}

export function parseChannelEnsureRequest(value: unknown): ChannelEnsureRequest {
	const record = requireRecord(value, "channel.ensure request");
	return {
		name: requireString(record, "name"),
		displayName: optionalString(record, "displayName"),
		description: optionalString(record, "description"),
		topic: optionalString(record, "topic"),
	};
}

function parseInputAttachment(value: unknown): InputAttachmentDocument {
	const record = requireRecord(value, "input attachment");
	return {
		platform: optionalString(record, "platform"),
		fileID: optionalString(record, "fileID"),
		messageID: optionalString(record, "messageID"),
		filename: optionalString(record, "filename"),
		contentType: optionalString(record, "contentType"),
		sizeBytes: optionalNumber(record, "sizeBytes"),
	};
}

export function parseAttachmentImportRequest(value: unknown): AttachmentImportRequest {
	const record = requireRecord(value, "attachments.import request");
	return {
		messageID: requireString(record, "messageID"),
		targetDirectoryPath: requireString(record, "targetDirectoryPath"),
		inputAttachments: optionalArray(record, "inputAttachments").map(parseInputAttachment),
	};
}

export function parseIdentityResolveRequest(value: unknown): IdentityResolveRequest {
	const record = requireRecord(value, "identity.resolve request");
	return { senderID: requireString(record, "senderID") };
}
