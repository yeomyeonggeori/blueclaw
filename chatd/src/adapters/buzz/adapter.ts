import {
	BaseFormatConverter,
	defaultEmojiResolver,
	Message,
	parseMarkdown,
	stringifyMarkdown,
	type Adapter,
	type AdapterPostableMessage,
	type Attachment,
	type Author,
	type ChatInstance,
	type FetchOptions,
	type FetchResult,
	type RawMessage,
	type Root,
	type ThreadInfo,
	type UserInfo,
	type WebhookOptions,
} from "chat";
import {
	canonicalChannelName,
	type ManagedChannel,
	type ManagedChannelSpec,
} from "../../channels.ts";
import { isServedByTheRelay } from "./blossom.ts";
import { createBuzzRelayClient, type BuzzRelayClient } from "./relay-client.ts";
import {
	BUZZ_ADAPTER_NAME,
	firstTagValue,
	threadTagsOf,
	type BuzzAdapterConfig,
	type BuzzChannel,
	type BuzzEvent,
	type BuzzThreadId,
} from "./types.ts";
import type { ReactionSummary } from "../../visible-context.ts";
import type { OutgoingAttachment } from "../../outgoing-attachment.ts";
import { buildMessageBody, threadRootOf } from "./user-session.ts";
import { originOfTags } from "../../mirror/origin.ts";
import { reactionContentOf } from "../../mirror/reaction-emoji.ts";

const STREAM_MESSAGE_KIND = 9;
const TYPING_INDICATOR_KIND = 20002;

function encodeCreatedAtCursor(createdAt: number): string {
	return String(createdAt);
}

function decodeCreatedAtCursor(cursor?: string): number | undefined {
	if (!cursor) return undefined;
	const parsed = Number.parseInt(cursor, 10);
	return Number.isFinite(parsed) ? parsed : undefined;
}

const REACTION_KIND = 7;
const PROFILE_KIND = 0;
const GROUP_METADATA_KIND = 39000;
const GROUP_MEMBERS_KIND = 39002;
const CREATE_CHANNEL_KIND = 9007;
const SET_TOPIC_KIND = 9002;
const EDIT_MESSAGE_KIND = 40003;
const DELETE_MESSAGE_KIND = 9005;
const MEMBER_ADDED_NOTIFICATION_KIND = 44100;
const MEMBER_REMOVED_NOTIFICATION_KIND = 44101;


function attachmentsFromEvent(event: BuzzEvent): Attachment[] {
	const filenamesByURL = filenamesFromBody(event.content);
	const attachments: Attachment[] = [];
	for (const tag of event.tags) {
		if (tag[0] !== "imeta") continue;
		const fields = imetaFieldsOf(tag);
		if (!fields.url) continue;
		const isImage = fields.mimeType?.startsWith("image/") ?? false;
		attachments.push({
			type: isImage ? "image" : "file",
			url: fields.url,
			mimeType: fields.mimeType,
			size: fields.size,
			name: filenamesByURL.get(fields.url) ?? basenameOf(fields.url),
		});
	}
	return attachments;
}

function imetaFieldsOf(tag: string[]): { url?: string; mimeType?: string; size?: number } {
	const fields: { url?: string; mimeType?: string; size?: number } = {};
	for (const entry of tag.slice(1)) {
		const separatorIndex = entry.indexOf(" ");
		if (separatorIndex < 0) continue;
		const key = entry.slice(0, separatorIndex);
		const value = entry.slice(separatorIndex + 1);
		if (key === "url") fields.url = value;
		else if (key === "m") fields.mimeType = value;
		else if (key === "size") fields.size = Number(value) || undefined;
	}
	return fields;
}

function filenamesFromBody(body: string): Map<string, string> {
	const filenames = new Map<string, string>();
	const pattern = /!?\[([^\]]*)\]\((\S+?)\)/g;
	for (const match of body.matchAll(pattern)) {
		const [, label, url] = match;
		if (url && label) filenames.set(url, label);
	}
	return filenames;
}

function basenameOf(url: string): string {
	const withoutQuery = url.split("?")[0] ?? url;
	const segments = withoutQuery.split("/");
	return segments[segments.length - 1] || "attachment";
}

function reactionDisplayOf(event: BuzzEvent): { emoji: string; imageURL?: string } {
	const content = event.content.trim();
	const shortcode =
		content.startsWith(":") && content.endsWith(":") ? content.slice(1, -1) : content;
	const emojiTag = event.tags.find((tag) => tag[0] === "emoji" && tag[1] === shortcode);
	if (emojiTag?.[2]) return { emoji: content, imageURL: emojiTag[2] };
	// A bare emoji name (e.g. white_check_mark, mirrored from Mattermost) resolves
	// to its unicode; content that is already unicode is not a shortcode name and
	// passes through untouched.
	if (/^[a-z0-9_+-]+$/i.test(shortcode)) return { emoji: reactionContentOf(shortcode) };
	return { emoji: content };
}

function pubkeyTagValuesOf(raw: unknown): string[] {
	if (typeof raw !== "object" || raw === null || !("tags" in raw)) return [];
	const { tags } = raw;
	if (!Array.isArray(tags)) return [];
	const pubkeys: string[] = [];
	for (const tag of tags) {
		if (Array.isArray(tag) && tag[0] === "p" && typeof tag[1] === "string") {
			pubkeys.push(tag[1]);
		}
	}
	return pubkeys;
}

class BuzzFormatConverter extends BaseFormatConverter {
	toAst(platformText: string): Root {
		return parseMarkdown(platformText);
	}

	fromAst(ast: Root): string {
		return stringifyMarkdown(ast);
	}
}

// A relay subscription hands each event to a callback that cannot await it, so a
// failure here has nowhere to go but the process. One message the agent could
// not take is not a reason to stop taking the rest.
function reportBuzzFailure(doing: string, reason: unknown): void {
	console.error(`[buzz] ${doing} failed:`, reason instanceof Error ? reason.message : reason);
}

export class BuzzAdapter implements Adapter<BuzzThreadId, BuzzEvent> {
	readonly name = BUZZ_ADAPTER_NAME;
	readonly userName: string;
	private readonly config: BuzzAdapterConfig;
	private readonly relay: BuzzRelayClient;
	private readonly converter = new BuzzFormatConverter();
	private chat: ChatInstance | null = null;
	private channelsById = new Map<string, BuzzChannel>();
	private subscribedChannelIds = new Set<string>();
	private profileByPubkey = new Map<string, { name?: string; nip05?: string; picture?: string }>();

	constructor(config: BuzzAdapterConfig) {
		this.config = config;
		this.userName = config.botDisplayName;
		this.relay = createBuzzRelayClient(config.relayURL, config.privateKeyHex, config.authTagJSON);
	}

	renderFormatted(content: Root): string {
		return this.converter.fromAst(content);
	}

	get botPubkey(): string {
		return this.relay.pubkeyHex;
	}

	channelIdFromThreadId(threadId: string): string {
		return this.decodeThreadId(threadId).channelId;
	}

	async channelIdByName(name: string): Promise<string | undefined> {
		return this.findChannelIdByName(canonicalChannelName(name));
	}

	async fetchAttachment(attachment: { url?: string }): Promise<Response> {
		const address = attachment.url ?? "";
		if (!isServedByTheRelay(address, this.config.relayURL)) {
			return new Response("attachment url is not served by this relay", { status: 400 });
		}
		return fetch(address);
	}

	// Somebody who writes under a root is answering that root, so what they wrote
	// is read against it and not against whatever else the channel holds. A
	// message that is its own root has no thread yet, and reads the channel.
	// A direct conversation threads the same way a channel does.
	historyScopeThreadId(threadId: string, messageId: string): string {
		const decoded = this.decodeThreadId(threadId);
		if (!decoded.rootEventId || decoded.rootEventId === messageId) {
			return this.encodeThreadId({ channelId: decoded.channelId });
		}
		return threadId;
	}

	threadRootIdOf(raw: unknown): string | undefined {
		if (typeof raw !== "object" || raw === null || !("tags" in raw)) return undefined;
		return threadTagsOf(raw as BuzzEvent).rootEventId;
	}

	addressingOf(raw: unknown): { botMentioned: boolean; otherPersonMentioned: boolean } {
		const mentionedPubkeys = pubkeyTagValuesOf(raw);
		return {
			botMentioned: mentionedPubkeys.includes(this.relay.pubkeyHex),
			otherPersonMentioned: mentionedPubkeys.some((pubkey) => pubkey !== this.relay.pubkeyHex),
		};
	}

	encodeThreadId(data: BuzzThreadId): string {
		if (!data.rootEventId) return `${BUZZ_ADAPTER_NAME}:${data.channelId}`;
		return `${BUZZ_ADAPTER_NAME}:${data.channelId}:${data.rootEventId}`;
	}

	decodeThreadId(threadId: string): BuzzThreadId {
		const parts = threadId.split(":");
		const channelId = parts[1];
		if (parts[0] !== BUZZ_ADAPTER_NAME || !channelId) {
			throw new Error(`invalid buzz thread id: ${threadId}`);
		}
		return { channelId, rootEventId: parts[2] || undefined };
	}

	async initialize(chat: ChatInstance): Promise<void> {
		this.chat = chat;
		await this.relay.connect();
		await this.refreshChannels();
		this.subscribeToChannels();
		this.subscribeToMembershipChanges();
	}

	async ensureChannel(spec: ManagedChannelSpec): Promise<ManagedChannel> {
		const name = canonicalChannelName(spec.name);
		const existingChannelId = await this.findChannelIdByName(name);
		if (existingChannelId) {
			return this.managedChannel(existingChannelId, false);
		}
		const channelId = crypto.randomUUID();
		const tags: string[][] = [
			["h", channelId],
			["name", name],
			["visibility", "open"],
			["channel_type", "stream"],
		];
		if (spec.description) tags.push(["about", spec.description]);
		await this.relay.publish(CREATE_CHANNEL_KIND, "", tags);
		if (spec.topic) {
			await this.relay.publish(SET_TOPIC_KIND, "", [
				["h", channelId],
				["topic", spec.topic],
			]);
		}
		this.channelsById.set(channelId, { channelId, name, isDM: false });
		this.subscribeToChannels();
		return this.managedChannel(channelId, true);
	}

	private managedChannel(channelId: string, created: boolean): ManagedChannel {
		return { channelID: channelId, replyTargetID: this.encodeThreadId({ channelId }), created };
	}

	private async findChannelIdByName(name: string): Promise<string | undefined> {
		const metadataEvents = await this.relay.query({ kinds: [GROUP_METADATA_KIND], limit: 500 });
		const latestByChannel = new Map<string, BuzzEvent>();
		for (const event of metadataEvents) {
			const channelId = firstTagValue(event, "d");
			if (!channelId) continue;
			const known = latestByChannel.get(channelId);
			if (!known || event.created_at > known.created_at) latestByChannel.set(channelId, event);
		}
		for (const [channelId, event] of latestByChannel) {
			if (firstTagValue(event, "archived") === "true") continue;
			if (canonicalChannelName(firstTagValue(event, "name") ?? "") === name) return channelId;
		}
		return undefined;
	}

	private subscribeToMembershipChanges(): void {
		this.relay.subscribe(
			[
				{
					kinds: [MEMBER_ADDED_NOTIFICATION_KIND, MEMBER_REMOVED_NOTIFICATION_KIND],
					"#p": [this.relay.pubkeyHex],
					since: Math.floor(Date.now() / 1000),
				},
			],
			() => {
				void this.refreshChannels()
					.then(() => this.subscribeToChannels())
					.catch((reason) => reportBuzzFailure("refreshing channels", reason));
			},
		);
	}

	async disconnect(): Promise<void> {
		this.relay.disconnect();
	}

	async handleWebhook(_request: Request, _options?: WebhookOptions): Promise<Response> {
		return new Response("OK", { status: 200 });
	}

	private async refreshChannels(): Promise<void> {
		const memberships = await this.relay.query({
			kinds: [GROUP_MEMBERS_KIND],
			"#p": [this.relay.pubkeyHex],
		});
		const channelIds = [
			...new Set(memberships.map((event) => firstTagValue(event, "d")).filter((id): id is string => Boolean(id))),
		];
		if (channelIds.length === 0) return;
		const metadataEvents = await this.relay.query({ kinds: [GROUP_METADATA_KIND], "#d": channelIds });
		const latestMetadata = new Map<string, BuzzEvent>();
		for (const event of metadataEvents) {
			const channelId = firstTagValue(event, "d");
			if (!channelId) continue;
			const known = latestMetadata.get(channelId);
			if (!known || event.created_at > known.created_at) latestMetadata.set(channelId, event);
		}
		for (const channelId of channelIds) {
			const metadata = latestMetadata.get(channelId);
			this.channelsById.set(channelId, {
				channelId,
				name: metadata ? (firstTagValue(metadata, "name") ?? "") : "",
				isDM: metadata ? firstTagValue(metadata, "t") === "dm" : false,
			});
		}
	}

	private subscribeToChannels(): void {
		for (const channelId of this.channelsById.keys()) {
			if (this.subscribedChannelIds.has(channelId)) continue;
			this.subscribedChannelIds.add(channelId);
			this.relay.subscribe(
				[{ kinds: [STREAM_MESSAGE_KIND], "#h": [channelId], since: Math.floor(Date.now() / 1000) }],
				(event) => {
					void this.dispatchIncomingEvent(event).catch((reason) =>
						reportBuzzFailure(`handling message ${event.id}`, reason),
					);
				},
			);
			if (this.config.mirror) {
				this.relay.subscribe(
					[
						{
							kinds: [REACTION_KIND, EDIT_MESSAGE_KIND, DELETE_MESSAGE_KIND],
							"#h": [channelId],
							since: Math.floor(Date.now() / 1000),
						},
					],
					(event) => this.emitMirrorControlEvent(event, channelId),
				);
			}
		}
	}

	private emitMirrorControlEvent(event: BuzzEvent, channelId: string): void {
		const mirror = this.config.mirror;
		if (!mirror || event.pubkey === this.relay.pubkeyHex) return;
		const targetEventId = firstTagValue(event, "e");
		if (!targetEventId) return;
		const origin = originOfTags(event.tags);
		void this.linkedAccountEmail(event.pubkey)
			.then((senderEmail) => {
				if (event.kind === EDIT_MESSAGE_KIND) {
					mirror.edit({ targetEventId, buzzChannelId: channelId, text: event.content, senderEmail, origin });
				} else if (event.kind === DELETE_MESSAGE_KIND) {
					mirror.remove({ targetEventId, buzzChannelId: channelId, senderEmail, origin });
				} else if (event.kind === REACTION_KIND) {
					mirror.react({ targetEventId, buzzChannelId: channelId, emoji: event.content, senderEmail, origin });
				}
			})
			.catch(() => void 0);
	}

	private async dispatchIncomingEvent(event: BuzzEvent): Promise<void> {
		if (!this.chat || event.pubkey === this.relay.pubkeyHex) return;
		const channelId = firstTagValue(event, "h");
		if (!channelId) return;
		if (!this.channelsById.has(channelId)) {
			await this.refreshChannels();
			this.subscribeToChannels();
		}
		this.emitMirrorInbound(event, channelId);
		const threadId = this.threadIdForEvent(event);
		await this.chat.processMessage(this, threadId, async () => await this.messageFromEvent(event));
	}

	private emitMirrorInbound(event: BuzzEvent, channelId: string): void {
		const mirror = this.config.mirror;
		if (!mirror) return;
		const { rootEventId } = threadTagsOf(event);
		void Promise.all([this.fetchProfile(event.pubkey), this.linkedAccountEmail(event.pubkey)])
			.then(([profile, email]) => {
				mirror.message({
					buzzEventId: event.id,
					buzzChannelId: channelId,
					text: event.content,
					senderPubkey: event.pubkey,
					senderName: profile.name ?? `npub…${event.pubkey.slice(-6)}`,
					senderEmail: email,
					origin: originOfTags(event.tags),
					replyToBuzzEventId: rootEventId && rootEventId !== event.id ? rootEventId : undefined,
				});
			})
			.catch(() => void 0);
	}

	private threadIdForEvent(event: BuzzEvent): string {
		const channelId = firstTagValue(event, "h") ?? "";
		const { rootEventId } = threadTagsOf(event);
		return this.encodeThreadId({ channelId, rootEventId: rootEventId ?? event.id });
	}

	parseMessage(raw: BuzzEvent): Message<BuzzEvent> {
		const cachedProfile = this.profileByPubkey.get(raw.pubkey);
		return this.buildMessage(raw, cachedProfile);
	}

	private async messageFromEvent(event: BuzzEvent): Promise<Message<BuzzEvent>> {
		return this.buildMessage(event, await this.fetchProfile(event.pubkey));
	}

	private buildMessage(
		event: BuzzEvent,
		profile: { name?: string; nip05?: string } | undefined,
	): Message<BuzzEvent> {
		return new Message({
			id: event.id,
			threadId: this.threadIdForEvent(event),
			text: event.content,
			formatted: this.converter.toAst(event.content),
			raw: event,
			author: this.authorForPubkey(event.pubkey, profile),
			metadata: { dateSent: new Date(event.created_at * 1000), edited: false },
			attachments: attachmentsFromEvent(event),
		});
	}

	private authorForPubkey(pubkey: string, profile?: { name?: string; nip05?: string }): Author {
		const displayName = profile?.name?.trim() || `npub…${pubkey.slice(-6)}`;
		return {
			userId: pubkey,
			userName: displayName,
			fullName: displayName,
			isBot: pubkey === this.relay.pubkeyHex,
			isMe: pubkey === this.relay.pubkeyHex,
		};
	}

	senderAvatarUrlOf(senderId: string): string | undefined {
		return this.profileByPubkey.get(senderId)?.picture;
	}

	private async fetchProfile(pubkey: string): Promise<{ name?: string; nip05?: string; picture?: string }> {
		const cached = this.profileByPubkey.get(pubkey);
		if (cached) return cached;
		const events = await this.relay.query({ kinds: [PROFILE_KIND], authors: [pubkey], limit: 1 });
		let profile: { name?: string; nip05?: string; picture?: string } = {};
		const content = events.at(-1)?.content;
		if (content) {
			try {
				const parsed = JSON.parse(content) as Record<string, unknown>;
				profile = {
					name: typeof parsed.display_name === "string" && parsed.display_name.trim() !== ""
						? parsed.display_name
						: typeof parsed.name === "string"
							? parsed.name
							: undefined,
					nip05: typeof parsed.nip05 === "string" ? parsed.nip05 : undefined,
					picture: typeof parsed.picture === "string" ? parsed.picture : undefined,
				};
			} catch {
				profile = {};
			}
		}
		this.profileByPubkey.set(pubkey, profile);
		return profile;
	}

	async postMessage(
		threadId: string,
		message: AdapterPostableMessage,
		extraTags: string[][] = [],
	): Promise<RawMessage<BuzzEvent>> {
		const decoded = this.decodeThreadId(threadId);
		const { body, mediaTags } = await this.renderPostableWithFiles(message);
		const tags: string[][] = [["h", decoded.channelId], ...mediaTags];
		tags.push(...(await this.threadTags(decoded, extraTags)));
		const event = await this.relay.publish(STREAM_MESSAGE_KIND, body, tags);
		return { id: event.id, threadId, raw: event };
	}

	// The base renderer keeps a postable's text and quietly drops its files, so
	// an attachment used to vanish behind a green "sent". Files travel the same
	// way a person's do: uploaded to the relay's media store, linked from the
	// body, named in imeta tags. A refused upload throws instead of shrinking
	// the message.
	private async renderPostableWithFiles(
		message: AdapterPostableMessage,
	): Promise<{ body: string; mediaTags: string[][] }> {
		const files = typeof message === "object" && "files" in message ? (message.files ?? []) : [];
		const text = this.converter.renderPostable(message);
		if (files.length === 0) return { body: text, mediaTags: [] };
		const attachments: OutgoingAttachment[] = [];
		for (const file of files) {
			attachments.push({
				filename: file.filename,
				contentType: file.mimeType ?? "application/octet-stream",
				contentBase64: (file.data instanceof Buffer
					? file.data
					: Buffer.from(new Uint8Array(file.data instanceof Blob ? await file.data.arrayBuffer() : file.data))
				).toString("base64"),
			});
		}
		return await buildMessageBody({
			relayURL: this.config.relayURL,
			userSecretHex: this.config.privateKeyHex,
			message: text,
			attachments,
		});
	}

	// A conversation is a message and the replies to it, and nothing deeper.
	// Answering a reply answers what it replied to, so every answer lands flat
	// under one root instead of opening a thread inside a thread. A direct
	// conversation is already a conversation: no thread tags at all.
	private async threadTags(
		decoded: BuzzThreadId,
		extraTags: string[][],
	): Promise<string[][]> {
		const plainTags = extraTags.filter((tag) => tag[0] !== "e");
		if (this.channelsById.get(decoded.channelId)?.isDM) {
			return plainTags;
		}
		const answeredId = extraTags.find((tag) => tag[0] === "e")?.[1];
		const anchorId = decoded.rootEventId ?? answeredId;
		if (!anchorId) return plainTags;
		const rootId = await threadRootOf(this.relay, anchorId);
		if (!rootId) return plainTags;
		return [...plainTags, ["e", rootId, "", "root"], ["e", rootId, "", "reply"]];
	}

	async postChannelMessage(channelId: string, message: AdapterPostableMessage): Promise<RawMessage<BuzzEvent>> {
		return this.postMessage(this.encodeThreadId({ channelId }), message);
	}

	async editMessage(
		threadId: string,
		messageId: string,
		message: AdapterPostableMessage,
	): Promise<RawMessage<BuzzEvent>> {
		const decoded = this.decodeThreadId(threadId);
		const text = this.converter.renderPostable(message);
		const event = await this.relay.publish(EDIT_MESSAGE_KIND, text, [
			["h", decoded.channelId],
			["e", messageId],
		]);
		return { id: messageId, threadId, raw: event };
	}

	async deleteMessage(threadId: string, messageId: string): Promise<void> {
		await this.relay.publish(DELETE_MESSAGE_KIND, "", [
			["h", this.decodeThreadId(threadId).channelId],
			["e", messageId],
		]);
	}

	async addReaction(threadId: string, messageId: string, emoji: string): Promise<void> {
		const tags: string[][] = [["e", messageId]];
		if (threadId) {
			tags.push(["h", this.decodeThreadId(threadId).channelId]);
		}
		await this.relay.publish(REACTION_KIND, reactionContentOf(String(emoji)), tags);
	}

	async removeReaction(): Promise<void> {}

	async startTyping(threadId: string, _status?: string): Promise<void> {
		const decoded = this.decodeThreadId(threadId);
		const tags: string[][] = [["h", decoded.channelId]];
		if (decoded.rootEventId) tags.push(["e", decoded.rootEventId, "", "root"]);
		await this.relay.publish(TYPING_INDICATOR_KIND, "", tags).catch(() => void 0);
	}

	async fetchMessages(threadId: string, options?: FetchOptions): Promise<FetchResult<BuzzEvent>> {
		const decoded = this.decodeThreadId(threadId);
		const limit = options?.limit && options.limit > 0 ? options.limit : 20;
		if (decoded.rootEventId) return this.fetchThreadMessages(decoded.channelId, decoded.rootEventId, limit);
		const until = decodeCreatedAtCursor(options?.cursor);
		const filter: Record<string, unknown> = {
			kinds: [STREAM_MESSAGE_KIND],
			"#h": [decoded.channelId],
			limit,
		};
		if (until !== undefined) filter.until = until;
		const [events, deleted] = await Promise.all([
			this.relay.query(filter),
			this.deletedMessageIds(decoded.channelId, limit),
		]);
		const chronological = events
			.filter((event) => !deleted.has(event.id))
			.sort((first, second) => first.created_at - second.created_at);
		const messages: Message<BuzzEvent>[] = [];
		for (const event of chronological) {
			messages.push(this.buildMessage(event, await this.fetchProfile(event.pubkey)));
		}
		const oldest = chronological[0];
		const nextCursor =
			oldest && events.length >= limit ? encodeCreatedAtCursor(oldest.created_at) : undefined;
		return { messages, nextCursor };
	}

	private async fetchThreadMessages(
		channelId: string,
		rootEventId: string,
		limit: number,
	): Promise<FetchResult<BuzzEvent>> {
		const [events, deleted] = await Promise.all([
			this.relay.query({
				kinds: [STREAM_MESSAGE_KIND],
				"#h": [channelId],
				limit: Math.max(limit * 3, limit),
			}),
			this.deletedMessageIds(channelId, Math.max(limit * 3, limit)),
		]);
		const chronological = events
			.filter((event) => !deleted.has(event.id))
			.sort((first, second) => first.created_at - second.created_at);
		const relevant = chronological.filter((event) => {
			const { rootEventId: eventRootId } = threadTagsOf(event);
			return event.id === rootEventId || eventRootId === rootEventId;
		});
		const messages: Message<BuzzEvent>[] = [];
		for (const event of relevant.slice(-limit)) {
			messages.push(this.buildMessage(event, await this.fetchProfile(event.pubkey)));
		}
		return { messages, nextCursor: undefined };
	}

	// Somebody deletes a message to unsay it. The relay keeps what was said and
	// records that it was taken back, so a reader that asks only for messages is
	// handed words their author withdrew.
	private async deletedMessageIds(channelId: string, limit: number): Promise<Set<string>> {
		const events = await this.relay
			.query({ kinds: [DELETE_MESSAGE_KIND], "#h": [channelId], limit: Math.max(limit * 3, limit) })
			.catch(() => []);
		const deleted = new Set<string>();
		for (const event of events) {
			for (const tag of event.tags) {
				if (tag[0] === "e" && tag[1]) deleted.add(tag[1]);
			}
		}
		return deleted;
	}

	async listPeople(excludePubkeyHex: string): Promise<{ id: string; name: string; avatarURL?: string }[]> {
		const profileEvents = await this.relay.query({ kinds: [PROFILE_KIND], limit: 500 });
		const latestByPubkey = new Map<string, BuzzEvent>();
		for (const event of profileEvents) {
			const known = latestByPubkey.get(event.pubkey);
			if (!known || event.created_at > known.created_at) latestByPubkey.set(event.pubkey, event);
		}
		const people: { id: string; name: string; avatarURL?: string }[] = [];
		for (const [pubkey, event] of latestByPubkey) {
			if (pubkey === excludePubkeyHex || pubkey === this.relay.pubkeyHex) continue;
			try {
				const parsed = JSON.parse(event.content) as {
					name?: string;
					display_name?: string;
					picture?: string;
				};
				const name = parsed.name ?? parsed.display_name;
				if (!name) continue;
				people.push({ id: pubkey, name, avatarURL: parsed.picture });
			} catch {
				continue;
			}
		}
		return people.sort((first, second) => first.name.localeCompare(second.name));
	}

	async fetchReactions(scopeThreadId: string): Promise<Map<string, ReactionSummary[]>> {
		const decoded = this.decodeThreadId(scopeThreadId);
		const events = await this.relay.query({
			kinds: [REACTION_KIND],
			"#h": [decoded.channelId],
			limit: 500,
		});
		const countsByMessage = new Map<string, Map<string, ReactionSummary>>();
		for (const event of events) {
			const targetId = firstTagValue(event, "e");
			if (!targetId) continue;
			const display = reactionDisplayOf(event);
			if (!display.emoji) continue;
			const perEmoji = countsByMessage.get(targetId) ?? new Map<string, ReactionSummary>();
			const existing = perEmoji.get(display.emoji);
			if (existing) existing.count += 1;
			else perEmoji.set(display.emoji, { emoji: display.emoji, count: 1, imageURL: display.imageURL });
			countsByMessage.set(targetId, perEmoji);
		}
		const summaries = new Map<string, ReactionSummary[]>();
		for (const [targetId, perEmoji] of countsByMessage) {
			summaries.set(targetId, [...perEmoji.values()]);
		}
		return summaries;
	}

	async fetchThread(threadId: string): Promise<ThreadInfo> {
		const decoded = this.decodeThreadId(threadId);
		const channel = this.channelsById.get(decoded.channelId);
		return {
			id: threadId,
			channelId: decoded.channelId,
			channelName: channel?.name,
			isDM: channel?.isDM ?? false,
			metadata: {},
		};
	}

	isDM(threadId: string): boolean {
		const decoded = this.decodeThreadId(threadId);
		return this.channelsById.get(decoded.channelId)?.isDM ?? false;
	}

	async getUser(userId: string): Promise<UserInfo | null> {
		const profile = await this.fetchProfile(userId);
		const linkedEmail = await this.linkedAccountEmail(userId);
		return {
			userId,
			userName: profile.name ?? userId.slice(0, 8),
			fullName: profile.name ?? userId.slice(0, 8),
			email: linkedEmail ?? profile.nip05,
			avatarUrl: profile.picture,
			isBot: userId === this.relay.pubkeyHex,
		};
	}

	private async linkedAccountEmail(pubkey: string): Promise<string | undefined> {
		const linksPath = this.config.accountLinksPath;
		if (!linksPath) return undefined;
		const linksFile = Bun.file(linksPath);
		if (!(await linksFile.exists())) return undefined;
		try {
			const links = (await linksFile.json()) as Record<string, unknown>;
			const email = links[pubkey.toLowerCase()];
			return typeof email === "string" && email.trim() !== "" ? email.trim().toLowerCase() : undefined;
		} catch {
			return undefined;
		}
	}
}
