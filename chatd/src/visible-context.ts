import { convertEmojiPlaceholders } from "chat";
import type { FetchOptions, ThreadInfo, UserInfo } from "chat";

export type AddressingDocument = {
	botMentioned: boolean;
	otherPersonMentioned: boolean;
};

export type ReactionSummary = {
	emoji: string;
	count: number;
	imageURL?: string;
};

export type MessageAttachmentDocument = {
	kind: "image" | "file";
	url: string;
	filename?: string;
	mimeType?: string;
	sizeBytes?: number;
};

export type CustomEmojiDocument = {
	name: string;
	url: string;
};

export type VisibleContextMessageDocument = {
	id: string;
	threadRootId?: string;
	speaker: string;
	speakerHandle?: string;
	senderId?: string;
	senderAvatarUrl?: string;
	text: string;
	sentAt?: string;
	isBot?: boolean;
	isError?: boolean;
	reactions?: ReactionSummary[];
	attachments?: MessageAttachmentDocument[];
	customEmoji?: CustomEmojiDocument[];
};

export type VisibleContextSenderDocument = {
	platform: string;
	senderID: string;
	handle?: string;
	email?: string;
	name?: string;
};

export type VisibleContextDocument = {
	messages: VisibleContextMessageDocument[];
	// Whether the messages are how other conversations in the same place opened,
	// rather than the conversation being continued.
	messagesOpenOtherExchanges: boolean;
	hasMoreBefore: boolean;
	historyCursor: string;
	sender?: VisibleContextSenderDocument;
	conversationType?: string;
	channelID?: string;
	channelName?: string;
};

type ContextMessage = {
	id: string;
	text: string;
	author: { userId: string; userName: string; fullName: string; isBot?: boolean | "unknown" };
	metadata: { dateSent: Date };
	attachments?: Array<{ type?: string; url?: string; name?: string; mimeType?: string; size?: number }>;
	raw?: unknown;
};

export type ContextCapableAdapter = {
	name: string;
	fetchMessages(
		threadId: string,
		options?: FetchOptions,
	): Promise<{ messages: ContextMessage[]; nextCursor?: string }>;
	fetchThread(threadId: string): Promise<ThreadInfo>;
	getUser(userId: string): Promise<UserInfo | null>;
	fetchReactions?(scopeThreadId: string): Promise<Map<string, ReactionSummary[]>>;
	threadRootIdOf?(raw: unknown): string | undefined;
	senderAvatarUrlOf?(senderId: string): string | undefined;
};

export type NormalizedPlatformAdapter = ContextCapableAdapter & {
	historyScopeThreadId(threadId: string, messageId: string): string;
	addressingOf(raw: unknown): AddressingDocument;
};

export type HistoryCursorState = {
	threadId: string;
	cursor?: string;
};

export function encodeHistoryCursor(state: HistoryCursorState): string {
	return Buffer.from(JSON.stringify(state), "utf8").toString("base64url");
}

export function decodeHistoryCursor(historyCursor: string): HistoryCursorState {
	try {
		const decoded: unknown = JSON.parse(Buffer.from(historyCursor, "base64url").toString("utf8"));
		if (
			typeof decoded === "object" &&
			decoded !== null &&
			"threadId" in decoded &&
			typeof decoded.threadId === "string"
		) {
			const cursor = "cursor" in decoded && typeof decoded.cursor === "string" ? decoded.cursor : undefined;
			return { threadId: decoded.threadId, cursor };
		}
	} catch {
		return { threadId: historyCursor };
	}
	return { threadId: historyCursor };
}

const DEFAULT_HISTORY_LIMIT = 20;

export async function buildVisibleContext(
	adapter: ContextCapableAdapter,
	scopeThreadId: string,
	options: {
		beforeMessageId?: string;
		senderId?: string;
		cursor?: string;
		limit?: number;
		onlyExchangeOpenings?: boolean;
	} = {},
): Promise<VisibleContextDocument> {
	const limit = options.limit && options.limit > 0 ? options.limit : DEFAULT_HISTORY_LIMIT;
	const [fetchResult, threadInfo, senderInfo, reactionsById] = await Promise.all([
		adapter.fetchMessages(scopeThreadId, { cursor: options.cursor, limit: limit + 1, direction: "backward" }),
		adapter.fetchThread(scopeThreadId).catch(() => null),
		options.senderId ? adapter.getUser(options.senderId).catch(() => null) : Promise.resolve(null),
		adapter.fetchReactions?.(scopeThreadId).catch(() => null) ?? Promise.resolve(null),
	]);
	let previousMessages = messagesBefore(fetchResult.messages, options.beforeMessageId).filter(
		(candidate) => !isProgressMessage(candidate.raw)
	);
	if (options.onlyExchangeOpenings) {
		// What another exchange opened with says what it is about. What was said
		// inside it belongs to whoever is in it, and read here it turns one
		// conversation into several that look like one.
		previousMessages = previousMessages.filter(
			(candidate) => (adapter.threadRootIdOf?.(candidate.raw) ?? candidate.id) === candidate.id
		);
	}
	const hasMoreBefore = Boolean(fetchResult.nextCursor) || previousMessages.length > limit;
	if (previousMessages.length > limit) {
		previousMessages = previousMessages.slice(-limit);
	}
	return {
		messagesOpenOtherExchanges: options.onlyExchangeOpenings === true,
		messages: previousMessages.map((message) =>
			toVisibleContextMessage(
				message,
				reactionsById?.get(message.id),
				adapter.threadRootIdOf?.(message.raw),
				adapter.senderAvatarUrlOf?.(message.author.userId),
			),
		),
		hasMoreBefore,
		historyCursor: encodeHistoryCursor({ threadId: scopeThreadId, cursor: fetchResult.nextCursor }),
		sender: senderInfo ? toContextSender(adapter.name, senderInfo) : undefined,
		conversationType: threadInfo ? (threadInfo.isDM ? "direct" : "channel") : undefined,
		channelID: threadInfo?.channelId,
		channelName: threadInfo?.channelName,
	};
}

export function emptyVisibleContext(scopeThreadId: string): VisibleContextDocument {
	return {
		messages: [],
		messagesOpenOtherExchanges: false,
		hasMoreBefore: true,
		historyCursor: encodeHistoryCursor({ threadId: scopeThreadId }),
	};
}

function messagesBefore(messages: ContextMessage[], beforeMessageId?: string): ContextMessage[] {
	if (!beforeMessageId) return [...messages];
	const boundaryIndex = messages.findIndex((message) => message.id === beforeMessageId);
	if (boundaryIndex >= 0) return messages.slice(0, boundaryIndex);
	return messages.filter((message) => message.id !== beforeMessageId);
}

function toVisibleContextMessage(
	message: ContextMessage,
	reactions?: ReactionSummary[],
	threadRootId?: string,
	senderAvatarUrl?: string,
): VisibleContextMessageDocument {
	return {
		id: message.id,
		// Which exchange the message belongs to, not who it answers. A message that
		// starts one belongs to itself, and leaving that out would put a root and
		// its own replies in different exchanges.
		threadRootId: threadRootId ?? message.id,
		speaker: message.author.fullName || message.author.userName,
		speakerHandle: message.author.userName,
		senderId: message.author.userId,
		senderAvatarUrl,
		text: convertEmojiPlaceholders(message.text, "gchat"),
		sentAt: message.metadata.dateSent.toISOString(),
		isBot: message.author.isBot === true,
		isError: isErrorMessage(message.raw),
		reactions: reactions && reactions.length > 0 ? reactions : undefined,
		attachments: attachmentsOf(message),
		customEmoji: customEmojiOf(message.raw),
	};
}

function customEmojiOf(raw: unknown): CustomEmojiDocument[] | undefined {
	if (typeof raw !== "object" || raw === null || !("tags" in raw)) return undefined;
	const { tags } = raw as { tags?: unknown };
	if (!Array.isArray(tags)) return undefined;
	const documents: CustomEmojiDocument[] = [];
	for (const tag of tags) {
		if (!Array.isArray(tag) || tag[0] !== "emoji") continue;
		const name = tag[1];
		const url = tag[2];
		if (typeof name === "string" && typeof url === "string" && name && url) {
			documents.push({ name, url });
		}
	}
	return documents.length > 0 ? documents : undefined;
}

function isErrorMessage(raw: unknown): boolean {
	if (typeof raw !== "object" || raw === null || !("tags" in raw)) return false;
	const { tags } = raw as { tags?: unknown };
	if (!Array.isArray(tags)) return false;
	return tags.some((tag) => Array.isArray(tag) && tag[0] === "reply-kind" && tag[1] === "error");
}

function attachmentsOf(message: ContextMessage): MessageAttachmentDocument[] | undefined {
	const documents: MessageAttachmentDocument[] = [];
	for (const attachment of message.attachments ?? []) {
		if (!attachment.url) continue;
		documents.push({
			kind: attachment.type === "image" ? "image" : "file",
			url: attachment.url,
			filename: attachment.name,
			mimeType: attachment.mimeType,
			sizeBytes: attachment.size,
		});
	}
	return documents.length > 0 ? documents : undefined;
}

function toContextSender(platform: string, user: UserInfo): VisibleContextSenderDocument {
	return {
		platform,
		senderID: user.userId,
		handle: user.userName,
		email: user.email,
		name: user.fullName || user.userName,
	};
}


// A turn writes what it is doing into a message somebody can watch. It is not
// something anyone said, so reading it back turns the agent's own working notes
// into conversation.
function isProgressMessage(raw: unknown): boolean {
	const tags = (raw as { tags?: unknown[] })?.tags;
	if (!Array.isArray(tags)) return false;
	return tags.some((tag) => Array.isArray(tag) && tag[0] === "reply-kind" && tag[1] === "progress");
}
