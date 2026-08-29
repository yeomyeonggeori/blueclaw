import { Buffer } from "node:buffer";
import { reactionContentOf } from "../../mirror/reaction-emoji.ts";
import {
	ConsoleLogger,
	EmojiResolver,
	Message,
	type Adapter,
	type AdapterPostableMessage,
	type Attachment,
	type Author,
	type ChannelInfo,
	type ChannelVisibility,
	type ChatInstance,
	type EphemeralMessage,
	type EmojiValue,
	type FetchOptions,
	type FetchResult,
	type FormattedContent,
	type Logger,
	type RawMessage,
	type ThreadInfo,
	type UserInfo,
	type WebhookOptions,
} from "chat";
import type {
	ActionsElement,
	ButtonElement,
	CardElement,
	SelectElement,
	RadioSelectElement,
} from "chat";
import {
	AuthenticationError,
	NetworkError,
	PermissionError,
	ResourceNotFoundError,
	ValidationError,
	cardToFallbackText,
	extractCard,
	extractFiles,
} from "@chat-adapter/shared";
import { MattermostFormatConverter } from "./format-converter";
import type {
	MattermostAdapterConfig,
	MattermostApiError,
	MattermostChannel,
	MattermostChannelType,
	MattermostCreatePostRequest,
	MattermostFileInfo,
	MattermostPost,
	MattermostPostsResponse,
	MattermostReaction,
	MattermostThreadId,
	MattermostUser,
	MattermostWebSocketEvent,
} from "./types";

const ADAPTER_NAME = "mattermost";
const DEFAULT_FETCH_LIMIT = 50;
const MAX_CHANNEL_PAGE_SIZE = 200;
const MAX_USER_CACHE_SIZE = 1000;
const MAX_CHANNEL_CACHE_SIZE = 1000;

export class MattermostAdapter implements Adapter<MattermostThreadId, MattermostPost> {
	readonly lockScope = "thread" as const;
	readonly name = ADAPTER_NAME;

	botUserId?: string;
	userName: string;

	private chat: ChatInstance | null = null;
	private readonly config: MattermostAdapterConfig;
	private readonly converter = new MattermostFormatConverter();
	private readonly emojiResolver = new EmojiResolver();
	private logger: Logger;
	private websocket: WebSocket | null = null;
	private connectPromise: Promise<void> | null = null;
	private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
	private reconnectAttempt = 0;
	private reconnectEnabled = true;
	private nextSocketSeq = 1;
	private readonly users = new Map<string, MattermostUser>();
	private readonly channels = new Map<string, MattermostChannel>();

	constructor(config: MattermostAdapterConfig) {
		this.validateConfig(config);
		this.config = {
			...config,
			baseUrl: this.normalizeBaseUrl(config.baseUrl),
		};
		this.userName = config.userName ?? "mattermost-bot";
		this.logger = config.logger ?? new ConsoleLogger("info", ADAPTER_NAME);
	}

	async connect(): Promise<void> {
		if (this.websocket?.readyState === WebSocket.OPEN) {
			return;
		}

		if (this.connectPromise) {
			return this.connectPromise;
		}

		this.reconnectEnabled = true;

		const connectPromise = this.openWebSocket().catch((error) => {
			if (this.connectPromise === connectPromise) {
				this.connectPromise = null;
			}

			throw error;
		});

		this.connectPromise = connectPromise;

		return this.connectPromise;
	}

	channelIdFromThreadId(threadId: string): string {
		return this.decodeThreadId(threadId).channelId;
	}

	encodeThreadId(data: MattermostThreadId): string {
		const channelSegment = Buffer.from(data.channelId).toString("base64url");

		if (!data.rootPostId) {
			return `${ADAPTER_NAME}:${channelSegment}`;
		}

		const rootSegment = Buffer.from(data.rootPostId).toString("base64url");

		return `${ADAPTER_NAME}:${channelSegment}:${rootSegment}`;
	}

	decodeThreadId(threadId: string): MattermostThreadId {
		const parts = threadId.split(":");
		const channelSegment = parts[1];

		if (parts.length < 2 || parts.length > 3 || parts[0] !== ADAPTER_NAME || channelSegment === undefined) {
			throw new ValidationError(ADAPTER_NAME, `Invalid Mattermost thread ID: ${threadId}`);
		}

		return {
			channelId: Buffer.from(channelSegment, "base64url").toString(),
			rootPostId: parts[2] ? Buffer.from(parts[2], "base64url").toString() : undefined,
		};
	}

	async initialize(chat: ChatInstance): Promise<void> {
		this.chat = chat;
		this.logger = chat.getLogger(ADAPTER_NAME);

		await this.fetchMe();

		if (this.isWebSocketEnabled()) {
			await this.connect();
		}
	}

	async disconnect(): Promise<void> {
		this.reconnectEnabled = false;
		this.connectPromise = null;

		if (this.reconnectTimer) {
			clearTimeout(this.reconnectTimer);
			this.reconnectTimer = null;
		}

		if (this.websocket) {
			this.websocket.close();
			this.websocket = null;
		}
	}

	async handleWebhook(request: Request, options?: WebhookOptions): Promise<Response> {
		if (!this.chat) {
			return new Response("OK", { status: 200 });
		}

		let body: Record<string, unknown>;

		try {
			body = (await request.json()) as Record<string, unknown>;
		} catch {
			return new Response("OK", { status: 200 });
		}

		const context = body.context as Record<string, unknown> | undefined;

		if (context?.action_id) {
			const callbackTask = this.handleActionCallback(body, options).catch((error) => {
				this.logger.error("Mattermost action callback failed", error);
			});
			options?.waitUntil?.(callbackTask);
		}

		return new Response("OK", { status: 200 });
	}

	parseMessage(raw: MattermostPost): Message<MattermostPost> {
		return this.messageFromPost(
			raw,
			this.getCachedValue(this.users, raw.user_id),
			this.detectMention(raw),
		);
	}

	async postMessage(
		threadId: string,
		message: AdapterPostableMessage,
	): Promise<RawMessage<MattermostPost>> {
		const decoded = this.decodeThreadId(threadId);
		const payload = await this.buildCreatePostPayload(
			decoded.channelId,
			message,
			decoded.rootPostId,
		);
		const post = await this.api<MattermostPost>("/posts", {
			method: "POST",
			body: JSON.stringify(payload),
		});

		return {
			id: post.id,
			raw: post,
			threadId: this.threadIdForPost(post),
		};
	}

	async postChannelMessage(
		channelId: string,
		message: AdapterPostableMessage,
	): Promise<RawMessage<MattermostPost>> {
		const payload = await this.buildCreatePostPayload(channelId, message);
		const post = await this.api<MattermostPost>("/posts", {
			method: "POST",
			body: JSON.stringify(payload),
		});

		return {
			id: post.id,
			raw: post,
			threadId: this.threadIdForPost(post),
		};
	}

	async postEphemeral(
		threadId: string,
		userId: string,
		message: AdapterPostableMessage,
	): Promise<EphemeralMessage<MattermostPost>> {
		const decoded = this.decodeThreadId(threadId);
		const payload = await this.buildCreatePostPayload(
			decoded.channelId,
			message,
			decoded.rootPostId,
		);
		const post = await this.api<MattermostPost>("/posts/ephemeral", {
			method: "POST",
			body: JSON.stringify({
				user_id: userId,
				post: payload,
			}),
		});

		return {
			id: post.id,
			raw: post,
			threadId: this.threadIdForPost(post),
			usedFallback: false,
		};
	}

	async editMessage(
		threadId: string,
		messageId: string,
		message: AdapterPostableMessage,
	): Promise<RawMessage<MattermostPost>> {
		const decoded = this.decodeThreadId(threadId);
		const files = extractFiles(message);

		if (files.length > 0) {
			throw new ValidationError(
				ADAPTER_NAME,
				"Editing Mattermost posts with new file uploads is not supported yet.",
			);
		}

		const existing = await this.getPost(messageId);
		const text = this.renderPostableText(message);
		const card = extractCard(message);
		const putBody: Record<string, unknown> = {
			...existing,
			channel_id: decoded.channelId,
			message: text,
		};

		if (card && this.config.callbackUrl) {
			putBody.props = {
				...existing.props,
				attachments: this.renderCardAttachments(card),
			};
		}

		const updated = await this.api<MattermostPost>(`/posts/${messageId}`, {
			method: "PUT",
			body: JSON.stringify(putBody),
		});

		return {
			id: updated.id,
			raw: updated,
			threadId: this.threadIdForPost(updated),
		};
	}

	async deleteMessage(_threadId: string, messageId: string): Promise<void> {
		await this.api<void>(`/posts/${messageId}`, {
			method: "DELETE",
		});
	}

	async addReaction(
		_threadId: string,
		messageId: string,
		emoji: EmojiValue | string,
	): Promise<void> {
		await this.api<void>("/reactions", {
			method: "POST",
			body: JSON.stringify({
				emoji_name: this.toMattermostEmojiName(emoji),
				post_id: messageId,
				user_id: this.requireBotUserId(),
			}),
		});
	}

	async removeReaction(
		_threadId: string,
		messageId: string,
		emoji: EmojiValue | string,
	): Promise<void> {
		const emojiName = encodeURIComponent(this.toMattermostEmojiName(emoji));
		const userId = this.requireBotUserId();

		await this.api<void>(`/users/${userId}/posts/${messageId}/reactions/${emojiName}`, {
			method: "DELETE",
		});
	}

	async fetchMessage(
		_threadId: string,
		messageId: string,
	): Promise<Message<MattermostPost> | null> {
		let post: MattermostPost;

		try {
			post = await this.getPost(messageId);
		} catch (error) {
			if (error instanceof ResourceNotFoundError) {
				return null;
			}

			throw error;
		}

		return this.buildMessage(post);
	}

	async fetchMessages(
		threadId: string,
		options?: FetchOptions,
	): Promise<FetchResult<MattermostPost>> {
		const decoded = this.decodeThreadId(threadId);

		if (!decoded.rootPostId) {
			return this.fetchChannelMessages(decoded.channelId, options);
		}

		const response = await this.api<MattermostPostsResponse>(
			`/posts/${decoded.rootPostId}/thread`,
		);
		const posts = this.sortPosts(response);
		const messages = await Promise.all(posts.map((post) => this.buildMessage(post)));

		return this.paginateMessages(messages, options);
	}

	async fetchChannelMessages(
		channelId: string,
		options?: FetchOptions,
	): Promise<FetchResult<MattermostPost>> {
		const page = this.parsePageCursor(options?.cursor);
		const limit = this.normalizeLimit(options?.limit);
		const query = new URLSearchParams({
			page: String(page),
			per_page: String(limit),
		});
		const response = await this.api<MattermostPostsResponse>(
			`/channels/${channelId}/posts?${query.toString()}`,
		);
		const posts = this.sortPosts(response);
		const messages = await Promise.all(posts.map((post) => this.buildMessage(post)));

		return {
			messages,
			nextCursor: response.has_next ? String(page + 1) : undefined,
		};
	}

	async fetchThread(threadId: string): Promise<ThreadInfo> {
		const decoded = this.decodeThreadId(threadId);
		const channel = await this.getChannel(decoded.channelId);

		return {
			id: threadId,
			channelId: channel.id,
			channelName: channel.display_name ?? channel.name,
			channelVisibility: this.visibilityForChannelType(channel.type),
			isDM: channel.type === "D",
			metadata: {
				channelType: channel.type,
				rootPostId: decoded.rootPostId,
			},
		};
	}

	async fetchAttachment(attachment: { fileID?: string; url?: string }): Promise<Response> {
		const fileID = attachment.fileID || this.fileIdOfUrl(attachment.url ?? "");
		if (!fileID) {
			return new Response("attachment has no fileID", { status: 400 });
		}
		return fetch(this.apiUrl(`/files/${fileID}`), {
			headers: { Authorization: `Bearer ${this.config.botToken}` },
		});
	}

	// The adapter minted the url from the file identity; reading it back parses
	// the adapter's own grammar, nothing looser.
	private fileIdOfUrl(address: string): string {
		const prefix = this.apiUrl("/files/");
		if (!address.startsWith(prefix)) return "";
		const fileID = address.slice(prefix.length);
		return fileID.includes("/") ? "" : fileID;
	}

	async fetchChannelInfo(channelId: string): Promise<ChannelInfo> {
		const channel = await this.getChannel(channelId);

		return {
			id: channel.id,
			isDM: channel.type === "D",
			name: channel.display_name ?? channel.name,
			channelVisibility: this.visibilityForChannelType(channel.type),
			metadata: {
				header: channel.header,
				purpose: channel.purpose,
				teamId: channel.team_id,
				type: channel.type,
			},
		};
	}

	async getUser(userId: string): Promise<UserInfo | null> {
		try {
			return this.userInfoFromUser(await this.getMattermostUser(userId));
		} catch (error) {
			if (error instanceof ResourceNotFoundError) {
				return null;
			}

			throw error;
		}
	}

	getChannelVisibility(threadId: string): ChannelVisibility {
		const channel = this.getCachedValue(this.channels, this.channelIdFromThreadId(threadId));

		return this.visibilityForChannelType(channel?.type);
	}

	isDM(threadId: string): boolean {
		return (
			this.getCachedValue(this.channels, this.channelIdFromThreadId(threadId))?.type === "D"
		);
	}

	async openDM(userId: string): Promise<string> {
		const channel = await this.api<MattermostChannel>("/channels/direct", {
			method: "POST",
			body: JSON.stringify([this.requireBotUserId(), userId]),
		});

		this.setCachedValue(this.channels, channel.id, channel, MAX_CHANNEL_CACHE_SIZE);

		return this.encodeThreadId({ channelId: channel.id });
	}

	async startTyping(threadId: string, _status?: string): Promise<void> {
		const decoded = this.decodeThreadId(threadId);

		await this.api<void>(`/users/${this.requireBotUserId()}/typing`, {
			method: "POST",
			body: JSON.stringify({
				channel_id: decoded.channelId,
				parent_id: decoded.rootPostId,
			}),
		});
	}

	renderFormatted(content: FormattedContent): string {
		return this.converter.fromAst(content);
	}

	private async buildMessage(
		post: MattermostPost,
		isMention = this.detectMention(post),
	): Promise<Message<MattermostPost>> {
		const user = await this.getMattermostUser(post.user_id).catch(() => undefined);

		return this.messageFromPost(post, user, isMention);
	}

	private messageFromPost(
		post: MattermostPost,
		user?: MattermostUser,
		isMention?: boolean,
	): Message<MattermostPost> {
		return new Message({
			id: post.id,
			threadId: this.threadIdForPost(post),
			text: post.message ?? "",
			formatted: this.converter.toAst(post.message ?? ""),
			raw: post,
			author: this.authorFromUser(user, post.user_id),
			isMention,
			metadata: {
				dateSent: new Date(post.create_at),
				edited: post.edit_at > 0,
				editedAt: post.edit_at > 0 ? new Date(post.edit_at) : undefined,
			},
			attachments: this.attachmentsFromPost(post),
		});
	}

	private async buildCreatePostPayload(
		channelId: string,
		message: AdapterPostableMessage,
		rootPostId?: string,
	): Promise<MattermostCreatePostRequest> {
		const fileIds = await this.uploadFiles(channelId, message);
		const card = extractCard(message);
		const payload: MattermostCreatePostRequest = {
			channel_id: channelId,
			message: this.renderPostableText(message),
		};

		if (rootPostId) {
			payload.root_id = rootPostId;
		}

		if (fileIds.length > 0) {
			payload.file_ids = fileIds;
		}

		if (card && this.config.callbackUrl) {
			payload.props = {
				...payload.props,
				attachments: this.renderCardAttachments(card),
			};
		}

		return payload;
	}

	private renderPostableText(message: AdapterPostableMessage): string {
		const card = extractCard(message);

		if (card) {
			return cardToFallbackText(card, {
				boldFormat: "**",
				lineBreak: "\n\n",
				platform: "slack",
			});
		}

		return this.converter.renderPostable(message);
	}

	private renderCardAttachments(card: CardElement): Record<string, unknown>[] {
		const actions = this.extractActionsFromCard(card);
		const attachment: Record<string, unknown> = {};

		if (card.title) {
			attachment.title = card.title;
		}

		if (card.subtitle) {
			attachment.pretext = card.subtitle;
		}

		const textParts: string[] = [];

		for (const child of card.children) {
			if (child.type === "text") {
				const content = child.content?.trim();
				if (content) {
					textParts.push(child.style === "bold" ? `**${content}**` : content);
				}
			}
		}

		if (textParts.length > 0) {
			attachment.text = textParts.join("\n");
		}

		if (card.imageUrl) {
			attachment.image_url = card.imageUrl;
		}

		if (actions.length > 0) {
			attachment.actions = actions;
		}

		return [attachment];
	}

	private extractActionsFromCard(card: CardElement): Record<string, unknown>[] {
		const actions: Record<string, unknown>[] = [];

		for (const child of card.children) {
			if (child.type === "actions") {
				actions.push(...this.convertActionsElement(child));
			}
		}

		return actions;
	}

	private convertActionsElement(element: ActionsElement): Record<string, unknown>[] {
		const results: Record<string, unknown>[] = [];

		for (const child of element.children) {
			if (child.type === "button") {
				results.push(this.convertButtonElement(child));
			} else if (child.type === "select") {
				results.push(this.convertSelectElement(child));
			} else if (child.type === "radio_select") {
				results.push(...this.convertRadioSelectElement(child));
			}
		}

		return results;
	}

	private convertButtonElement(button: ButtonElement): Record<string, unknown> {
		const action: Record<string, unknown> = {
			id: button.id,
			name: button.label,
			type: "button",
		};

		if (button.style) {
			action.style = this.mapButtonStyle(button.style);
		}

		if (this.config.callbackUrl) {
			const context: Record<string, unknown> = {
				action_id: button.id,
			};
			if (button.value) {
				context.action_value = button.value;
			}
			action.integration = {
				url: this.config.callbackUrl,
				context,
			};
		}

		return action;
	}

	private convertSelectElement(select: SelectElement): Record<string, unknown> {
		const action: Record<string, unknown> = {
			id: select.id,
			name: select.placeholder ?? select.label,
			type: "select",
			options: select.options.map((opt) => ({
				text: opt.label,
				value: opt.value,
			})),
		};

		if (this.config.callbackUrl) {
			action.integration = {
				url: this.config.callbackUrl,
				context: {
					action_id: select.id,
				},
			};
		}

		return action;
	}

	private convertRadioSelectElement(radio: RadioSelectElement): Record<string, unknown>[] {
		return radio.options.map((opt) => {
			const action: Record<string, unknown> = {
				id: `${radio.id}_${opt.value}`,
				name: opt.label,
				type: "button",
			};

			if (this.config.callbackUrl) {
				action.integration = {
					url: this.config.callbackUrl,
					context: {
						action_id: radio.id,
						action_value: opt.value,
					},
				};
			}

			return action;
		});
	}

	private mapButtonStyle(style: string): string {
		switch (style) {
			case "primary":
				return "primary";
			case "danger":
				return "danger";
			default:
				return "default";
		}
	}

	private async uploadFiles(
		channelId: string,
		message: AdapterPostableMessage,
	): Promise<string[]> {
		const files = extractFiles(message);

		if (files.length === 0) {
			return [];
		}

		const formData = new FormData();
		formData.set("channel_id", channelId);

		for (const file of files) {
			const blob = this.toBlob(file.data, file.mimeType);
			formData.append("files", blob, file.filename);
		}

		const response = await this.api<{ file_infos?: MattermostFileInfo[] }>("/files", {
			method: "POST",
			body: formData,
		});

		return response.file_infos?.map((file) => file.id) ?? [];
	}

	private attachmentsFromPost(post: MattermostPost): Attachment[] {
		const files = post.metadata?.files ?? post.metadata?.file_infos ?? [];

		return files.map((file) => {
			const type: Attachment["type"] =
				file.mime_type?.startsWith("image/") || file.height || file.width
					? "image"
					: "file";

			return {
				type,
				name: file.name,
				mimeType: file.mime_type,
				size: file.size,
				width: file.width,
				height: file.height,
				url: this.fileUrl(file.id),
			};
		});
	}

	private authorFromUser(user: MattermostUser | undefined, fallbackUserId: string): Author {
		const fullName = [user?.first_name, user?.last_name].filter(Boolean).join(" ").trim();
		const isBot: boolean | "unknown" = user?.is_bot ?? "unknown";

		return {
			userId: fallbackUserId,
			userName: user?.username ?? fallbackUserId,
			fullName: fullName || user?.nickname || user?.username || fallbackUserId,
			isBot,
			isMe: fallbackUserId === this.botUserId,
		};
	}

	private userInfoFromUser(user: MattermostUser): UserInfo {
		const fullName = [user.first_name, user.last_name].filter(Boolean).join(" ").trim();

		return {
			userId: user.id,
			userName: user.username,
			fullName: fullName || user.nickname || user.username || user.id,
			isBot: user.is_bot ?? false,
		};
	}

	private detectMention(post: MattermostPost, mentions?: unknown): boolean {
		const botUserId = this.botUserId;
		const botUserName = this.userName;

		if (!botUserId && !botUserName) {
			return false;
		}

		const mentionTokens = new Set<string>([
			...this.extractMentionTokens(mentions),
			...this.extractMentionTokens(post.props?.mentions),
			...this.extractMentionTokens(post.props?.mentioned_user_ids),
		]);

		if (botUserId && mentionTokens.has(botUserId)) {
			return true;
		}

		if (
			botUserName &&
			(mentionTokens.has(botUserName) || mentionTokens.has(`@${botUserName}`))
		) {
			return true;
		}

		if (!botUserName) {
			return false;
		}

		const escapedUserName = this.escapeRegex(botUserName);

		return new RegExp(`(^|\\s)@${escapedUserName}\\b`, "i").test(post.message ?? "");
	}

	private extractMentionTokens(value: unknown): string[] {
		if (!value) {
			return [];
		}

		if (Array.isArray(value)) {
			return value.filter((item): item is string => typeof item === "string");
		}

		if (typeof value === "string") {
			const trimmed = value.trim();

			if (!trimmed) {
				return [];
			}

			if (
				(trimmed.startsWith("[") && trimmed.endsWith("]")) ||
				(trimmed.startsWith("{") && trimmed.endsWith("}"))
			) {
				const parsed = this.parseJsonField<unknown>(trimmed);

				if (parsed) {
					return this.extractMentionTokens(parsed);
				}
			}

			return trimmed.split(/[\s,]+/).filter(Boolean);
		}

		if (typeof value === "object") {
			return Object.keys(value as Record<string, unknown>);
		}

		return [];
	}

	private threadIdForPost(post: MattermostPost): string {
		return this.encodeThreadId({
			channelId: post.channel_id,
			rootPostId: post.root_id || post.id,
		});
	}

	private sortPosts(response: MattermostPostsResponse): MattermostPost[] {
		const posts = response.order
			.map((id) => response.posts[id])
			.filter((post): post is MattermostPost => Boolean(post));

		return posts.sort((left, right) => left.create_at - right.create_at);
	}

	private paginateMessages(
		messages: Message<MattermostPost>[],
		options?: FetchOptions,
	): FetchResult<MattermostPost> {
		const direction = options?.direction ?? "backward";
		const limit = this.normalizeLimit(options?.limit);

		if (messages.length === 0) {
			return { messages: [] };
		}

		const cursorIndex = options?.cursor
			? messages.findIndex((message) => message.id === options.cursor)
			: -1;

		if (options?.cursor && cursorIndex === -1) {
			return { messages: [] };
		}

		if (direction === "forward") {
			const start = cursorIndex >= 0 ? cursorIndex + 1 : 0;
			const end = Math.min(messages.length, start + limit);
			const page = messages.slice(start, end);

			return {
				messages: page,
				nextCursor: end < messages.length ? page[page.length - 1]?.id : undefined,
			};
		}

		const end = cursorIndex >= 0 ? cursorIndex : messages.length;
		const start = Math.max(0, end - limit);
		const page = messages.slice(start, end);

		return {
			messages: page,
			nextCursor: start > 0 ? page[0]?.id : undefined,
		};
	}

	private async fetchMe(): Promise<void> {
		const me = await this.api<MattermostUser>("/users/me");

		this.botUserId = me.id;
		this.userName = me.username || this.userName;
		this.setCachedValue(this.users, me.id, me, MAX_USER_CACHE_SIZE);
	}

	private async getMattermostUser(userId: string): Promise<MattermostUser> {
		const cached = this.getCachedValue(this.users, userId);

		if (cached) {
			return cached;
		}

		const user = await this.api<MattermostUser>(`/users/${userId}`);
		this.setCachedValue(this.users, userId, user, MAX_USER_CACHE_SIZE);

		return user;
	}

	private async getPost(postId: string): Promise<MattermostPost> {
		return this.api<MattermostPost>(`/posts/${postId}`);
	}

	private async getChannel(channelId: string): Promise<MattermostChannel> {
		const cached = this.getCachedValue(this.channels, channelId);

		if (cached) {
			return cached;
		}

		const channel = await this.api<MattermostChannel>(`/channels/${channelId}`);
		this.setCachedValue(this.channels, channel.id, channel, MAX_CHANNEL_CACHE_SIZE);

		return channel;
	}

	private async openWebSocket(): Promise<void> {
		const socket = new WebSocket(this.webSocketUrl());
		this.websocket = socket;

		await new Promise<void>((resolve, reject) => {
			let settled = false;
			let authSeq = 0;
			let authenticated = false;
			const authTimeout = setTimeout(() => {
				finishReject(
					new NetworkError(
						ADAPTER_NAME,
						"Mattermost websocket authentication timed out.",
					),
				);
			}, 10000);

			const finishResolve = () => {
				if (settled) {
					return;
				}

				settled = true;
				clearTimeout(authTimeout);
				resolve();
			};

			const finishReject = (error: Error) => {
				if (settled) {
					return;
				}

				settled = true;
				clearTimeout(authTimeout);
				if (this.websocket === socket) {
					this.websocket = null;
					this.connectPromise = null;
				}
				if (
					socket.readyState === WebSocket.OPEN ||
					socket.readyState === WebSocket.CONNECTING
				) {
					socket.close();
				}
				reject(error);
			};

			socket.addEventListener("open", () => {
				authSeq = this.nextSocketSeq++;
				socket.send(
					JSON.stringify({
						seq: authSeq,
						action: "authentication_challenge",
						data: { token: this.config.botToken },
					}),
				);
			});

			socket.addEventListener("message", (event) => {
				const payload = this.parseWebSocketPayload(event.data);

				if (!payload) {
					return;
				}

				if (payload.seq_reply === authSeq) {
					if (payload.status === "OK") {
						authenticated = true;
						this.reconnectAttempt = 0;
						this.logger.info("Mattermost websocket connected");
						finishResolve();
						return;
					}

					finishReject(
						this.toWebSocketError(
							payload,
							"Mattermost websocket authentication failed.",
						),
					);
					return;
				}

				void this.handleWebSocketPayload(payload);
			});

			socket.addEventListener("error", () => {
				finishReject(
					new NetworkError(ADAPTER_NAME, "Mattermost websocket connection failed."),
				);
			});

			socket.addEventListener("close", () => {
				if (this.websocket === socket) {
					this.websocket = null;
					this.connectPromise = null;
				}

				if (!settled) {
					finishReject(
						new NetworkError(
							ADAPTER_NAME,
							"Mattermost websocket closed before authentication completed.",
						),
					);
					return;
				}

				this.logger.warn("Mattermost websocket closed");

				if (authenticated && this.reconnectEnabled) {
					this.scheduleReconnect();
				}
			});
		});
	}

	private scheduleReconnect(): void {
		if (this.reconnectTimer || this.connectPromise || !this.reconnectEnabled) {
			return;
		}

		const baseDelay = this.config.websocket?.reconnectDelayMs ?? 1000;
		const maxDelay = this.config.websocket?.maxReconnectDelayMs ?? 30000;
		const jitter = Math.random() * baseDelay;
		const delay = Math.min(baseDelay * 2 ** this.reconnectAttempt + jitter, maxDelay);

		this.reconnectAttempt += 1;
		this.reconnectTimer = setTimeout(() => {
			this.reconnectTimer = null;
			void this.connect().catch((error) => {
				this.logger.error("Mattermost websocket reconnect failed", error);
				this.scheduleReconnect();
			});
		}, delay);
	}

	private async handleActionCallback(
		body: Record<string, unknown>,
		options?: WebhookOptions,
	): Promise<void> {
		if (!this.chat) {
			return;
		}

		const rawContext = body.context;
		const context =
			rawContext && typeof rawContext === "object"
				? (rawContext as Record<string, unknown>)
				: {};

		const actionId = context.action_id as string;

		if (!actionId) {
			return;
		}

		const actionValue =
			(context.action_value as string | undefined) ??
			(context.selected_option as string | undefined);
		const userId = body.user_id as string;
		const postId = body.post_id as string;
		const channelId = body.channel_id as string;

		const [post, user] = await Promise.all([
			postId ? this.getPost(postId).catch(() => undefined) : Promise.resolve(undefined),
			this.getMattermostUser(userId).catch(() => undefined),
		]);
		const threadId = post ? this.threadIdForPost(post) : this.encodeThreadId({ channelId });

		await this.chat.processAction(
			{
				actionId,
				adapter: this,
				messageId: postId ?? "",
				raw: body,
				threadId,
				user: this.authorFromUser(user, userId),
				value: actionValue,
			},
			options,
		);
	}

	private async handleWebSocketPayload(payload: MattermostWebSocketEvent): Promise<void> {
		switch (payload.event) {
			case "posted":
			case "post_edited":
				await this.handlePostedEvent(payload);
				break;
			case "post_deleted":
				await this.handlePostDeletedEvent(payload);
				break;
			case "reaction_added":
				await this.handleReactionEvent(payload, true);
				break;
			case "reaction_removed":
				await this.handleReactionEvent(payload, false);
				break;
			default:
				break;
		}
	}

	private async handlePostedEvent(payload: MattermostWebSocketEvent): Promise<void> {
		if (!this.chat) {
			return;
		}

		const rawPost = payload.data?.post;

		if (!rawPost) {
			return;
		}

		const post = this.parseEmbeddedJson<MattermostPost>(rawPost);

		if (!post || post.delete_at > 0 || post.type.startsWith("system_")) {
			return;
		}

		if (this.botUserId && post.user_id === this.botUserId) {
			this.emitMirrorPost(post, payload.event);
			return;
		}

		this.emitMirrorPost(post, payload.event);

		const channelType = payload.data?.channel_type;

		if (typeof channelType === "string" && this.isChannelType(channelType)) {
			const cachedChannel = this.getCachedValue(this.channels, post.channel_id);
			this.setCachedValue(
				this.channels,
				post.channel_id,
				cachedChannel
					? { ...cachedChannel, type: channelType }
					: {
							id: post.channel_id,
							name: post.channel_id,
							type: channelType,
						},
				MAX_CHANNEL_CACHE_SIZE,
			);
		}

		const senderName = payload.data?.sender_name;

		if (typeof senderName === "string" && !this.users.has(post.user_id)) {
			this.setCachedValue(
				this.users,
				post.user_id,
				{
					id: post.user_id,
					username: senderName,
				},
				MAX_USER_CACHE_SIZE,
			);
		}

		const isMention = this.detectMention(post, payload.data?.mentions);

		this.chat.processMessage(this, this.threadIdForPost(post), () =>
			this.buildMessage(post, isMention),
		);
	}

	private emitMirrorPost(post: MattermostPost, event: string | undefined): void {
		const mirror = this.config.mirror;
		if (!mirror || (event !== "posted" && event !== "post_edited")) return;
		void this.getMattermostUser(post.user_id)
			.then((user) => {
				if (event === "posted") {
					mirror.message({
						externalId: post.id,
						externalChannelId: post.channel_id,
						text: post.message ?? "",
						senderPlatformUserId: post.user_id,
						senderEmail: user.email,
						replyToExternalId: post.root_id || undefined,
					});
				} else {
					mirror.edit({
						externalId: post.id,
						externalChannelId: post.channel_id,
						text: post.message ?? "",
						senderPlatformUserId: post.user_id,
						senderEmail: user.email,
					});
				}
			})
			.catch((error) => this.logger.error("mirror post emit failed", error));
	}

	private async handlePostDeletedEvent(payload: MattermostWebSocketEvent): Promise<void> {
		const post = this.parseEmbeddedJson<MattermostPost>(payload.data?.post);

		if (!post) {
			return;
		}

		const mirror = this.config.mirror;
		if (mirror && !(this.botUserId && post.user_id === this.botUserId)) {
			void this.getMattermostUser(post.user_id)
				.then((user) =>
					mirror.remove({
						externalId: post.id,
						externalChannelId: post.channel_id,
						senderPlatformUserId: post.user_id,
						senderEmail: user.email,
					}),
				)
				.catch((error) => this.logger.error("mirror delete emit failed", error));
		}

		this.logger.debug(
			"Ignoring Mattermost post_deleted event; Chat SDK has no delete handler",
			{
				messageId: post.id,
				threadId: this.threadIdForPost(post),
			},
		);
	}

	private async handleReactionEvent(
		payload: MattermostWebSocketEvent,
		added: boolean,
	): Promise<void> {
		if (!this.chat) {
			return;
		}

		const rawReaction = payload.data?.reaction;

		if (!rawReaction) {
			return;
		}

		const reaction = this.parseEmbeddedJson<MattermostReaction>(rawReaction);

		if (!reaction) {
			return;
		}

		const post = await this.getPost(reaction.post_id).catch(() => undefined);
		const threadId = post
			? this.threadIdForPost(post)
			: this.encodeThreadId({
					channelId: payload.broadcast?.channel_id ?? "unknown",
					rootPostId: reaction.post_id,
				});
		const [user, message] = await Promise.all([
			this.getMattermostUser(reaction.user_id).catch(() => undefined),
			post ? this.buildMessage(post) : Promise.resolve(undefined),
		]);

		if (user) {
			this.setCachedValue(this.users, user.id, user, MAX_USER_CACHE_SIZE);
		}

		// The agent reacts as itself and those reactions cross like its messages do.
		// Its own are not filtered out here: what comes back from applying one is
		// caught by the echo suppressor, which compares the character both sides
		// carry rather than the name only this one uses.
		if (added && this.config.mirror && post) {
			this.config.mirror.react({
				externalId: reaction.post_id,
				externalChannelId: post.channel_id,
				emoji: reactionContentOf(reaction.emoji_name),
				senderPlatformUserId: reaction.user_id,
				senderEmail: user?.email,
			});
		}

		this.chat.processReaction({
			added,
			adapter: this,
			emoji: this.emojiResolver.fromSlack(reaction.emoji_name),
			message,
			messageId: reaction.post_id,
			raw: payload,
			rawEmoji: reaction.emoji_name,
			threadId,
			user: this.authorFromUser(user, reaction.user_id),
		});
	}

	private parseWebSocketPayload(data: unknown): MattermostWebSocketEvent | null {
		if (typeof data === "string") {
			return this.parseJsonField<MattermostWebSocketEvent>(data);
		}

		if (data instanceof ArrayBuffer) {
			return this.parseJsonField<MattermostWebSocketEvent>(Buffer.from(data).toString());
		}

		if (ArrayBuffer.isView(data)) {
			return this.parseJsonField<MattermostWebSocketEvent>(
				Buffer.from(data.buffer, data.byteOffset, data.byteLength).toString(),
			);
		}

		return null;
	}

	private parseJsonField<T>(value: string): T | null {
		try {
			return JSON.parse(value) as T;
		} catch {
			return null;
		}
	}

	private parseEmbeddedJson<T>(value: unknown): T | null {
		if (!value) {
			return null;
		}

		if (typeof value === "string") {
			return this.parseJsonField<T>(value);
		}

		if (typeof value === "object") {
			return value as T;
		}

		return null;
	}

	private toMattermostEmojiName(emoji: EmojiValue | string): string {
		const value = typeof emoji === "string" ? emoji : emoji.name;

		return value.replace(/^:|:$/g, "");
	}

	private visibilityForChannelType(channelType?: MattermostChannelType): ChannelVisibility {
		if (channelType === "O") {
			return "workspace";
		}

		if (channelType === "P" || channelType === "D" || channelType === "G") {
			return "private";
		}

		return "unknown";
	}

	private validateConfig(config: MattermostAdapterConfig): void {
		if (!config.baseUrl) {
			throw new ValidationError(ADAPTER_NAME, "Mattermost baseUrl is required.");
		}

		if (!config.botToken) {
			throw new ValidationError(ADAPTER_NAME, "Mattermost botToken is required.");
		}
	}

	private normalizeBaseUrl(baseUrl: string): string {
		return baseUrl.replace(/\/$/, "");
	}

	private apiUrl(path: string): string {
		const url = new URL(this.config.baseUrl);
		const basePath = url.pathname.replace(/\/$/, "");
		const normalizedPath = path.startsWith("/") ? path : `/${path}`;

		url.pathname = `${basePath}/api/v4${normalizedPath}`;

		return url.toString();
	}

	private webSocketUrl(): string {
		const url = new URL(this.config.baseUrl);
		const basePath = url.pathname.replace(/\/$/, "");

		url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
		url.pathname = `${basePath}/api/v4/websocket`;

		return url.toString();
	}

	private fileUrl(fileId: string): string {
		return this.apiUrl(`/files/${fileId}`);
	}

	private async api<T>(path: string, init: RequestInit = {}): Promise<T> {
		const headers = new Headers(init.headers);
		headers.set("Authorization", `Bearer ${this.config.botToken}`);
		headers.set("Accept", "application/json");

		if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
			headers.set("Content-Type", "application/json");
		}

		let response: Response;

		try {
			response = await fetch(this.apiUrl(path), {
				...init,
				headers,
			});
		} catch (error) {
			throw new NetworkError(
				ADAPTER_NAME,
				`Mattermost request failed for ${path}`,
				error instanceof Error ? error : undefined,
			);
		}

		if (!response.ok) {
			throw await this.toApiError(response, path);
		}

		if (response.status === 204) {
			return undefined as T;
		}

		return (await response.json()) as T;
	}

	private async toApiError(response: Response, path: string): Promise<Error> {
		const errorBody = (await response.json().catch(() => null)) as MattermostApiError | null;
		const message =
			errorBody?.message ||
			errorBody?.detailed_error ||
			`Mattermost API request failed for ${path}`;

		switch (response.status) {
			case 401:
				return new AuthenticationError(ADAPTER_NAME, message);
			case 403:
				return new PermissionError(ADAPTER_NAME, path);
			case 404:
				return new ResourceNotFoundError(ADAPTER_NAME, "resource", path);
			default:
				return new NetworkError(ADAPTER_NAME, message);
		}
	}

	private toWebSocketError(payload: MattermostWebSocketEvent, fallbackMessage: string): Error {
		const message = payload.error?.message || payload.error?.detailed_error || fallbackMessage;

		if (payload.error?.status_code === 401) {
			return new AuthenticationError(ADAPTER_NAME, message);
		}

		return new NetworkError(ADAPTER_NAME, message);
	}

	private requireBotUserId(): string {
		if (!this.botUserId) {
			throw new ValidationError(
				ADAPTER_NAME,
				"Bot user ID is not available. Call initialize() first.",
			);
		}

		return this.botUserId;
	}

	private normalizeLimit(limit?: number): number {
		if (!limit || limit < 1) {
			return DEFAULT_FETCH_LIMIT;
		}

		return Math.min(limit, MAX_CHANNEL_PAGE_SIZE);
	}

	private parsePageCursor(cursor?: string): number {
		if (!cursor) {
			return 0;
		}

		const page = Number(cursor);

		if (!Number.isInteger(page) || page < 0) {
			throw new ValidationError(ADAPTER_NAME, `Invalid Mattermost page cursor: ${cursor}`);
		}

		return page;
	}

	private toBlob(data: ArrayBuffer | Blob | Buffer, mimeType?: string): Blob {
		if (data instanceof Blob) {
			return data;
		}

		const arrayBuffer =
			data instanceof ArrayBuffer ? data.slice(0) : Uint8Array.from(data).buffer;

		return new Blob([arrayBuffer], mimeType ? { type: mimeType } : undefined);
	}

	private isChannelType(value: string): value is MattermostChannelType {
		return value === "O" || value === "P" || value === "D" || value === "G";
	}

	private escapeRegex(value: string): string {
		return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	}

	private getCachedValue<TKey, TValue>(cache: Map<TKey, TValue>, key: TKey): TValue | undefined {
		const value = cache.get(key);

		if (value === undefined) {
			return undefined;
		}

		cache.delete(key);
		cache.set(key, value);

		return value;
	}

	private setCachedValue<TKey, TValue>(
		cache: Map<TKey, TValue>,
		key: TKey,
		value: TValue,
		maxSize: number,
	): void {
		if (cache.has(key)) {
			cache.delete(key);
		}

		cache.set(key, value);

		while (cache.size > maxSize) {
			const oldestKey = cache.keys().next().value as TKey;
			cache.delete(oldestKey);
		}
	}

	private isWebSocketEnabled(): boolean {
		return this.config.websocket?.enabled ?? true;
	}
}
