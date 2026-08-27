import { isAlreadyKept, type OutgoingAttachment } from "../outgoing-attachment.ts";
import {
	CredentialRefused,
	requireMatchingCredential,
	UnsupportedByPlatform,
	type ActorCredential,
	type CredentialRequirement,
	type PersonalConversation,
	type PersonalEmoji,
	type IssuedCredential,
	type PersonalGateway,
	type PersonalIdentity,
	type PersonalImage,
	type PersonalMessage,
	type PersonalAttachment,
	type PersonalFile,
	type PersonalMessagePage,
	type PersonalPerson,
	type PersonalReaction,
} from "./gateway.ts";
import { withinBudget } from "./page-budget.ts";

type MattermostSession = { token: string; userID: string };
type MattermostChannel = { id: string; display_name: string; name: string; type: string };
type MattermostTeam = { id: string; name: string };
type MattermostUser = { id: string; username: string; first_name: string; last_name: string };
type MattermostReaction = { user_id: string; post_id: string; emoji_name: string };
type MattermostPost = {
	id: string;
	channel_id: string;
	root_id: string;
	user_id: string;
	message: string;
	create_at: number;
	edit_at: number;
	metadata?: { reactions?: MattermostReaction[]; files?: MattermostFileInfo[] };
};

type MattermostFileInfo = {
	id: string;
	name: string;
	mime_type: string;
	extension?: string;
	size: number;
	width?: number;
	height?: number;
};

const pageSize = 50;

export function createMattermostPersonalGateway(baseURL: string): PersonalGateway {
	return new MattermostPersonalGateway(baseURL);
}

class MattermostPersonalGateway implements PersonalGateway {
	readonly platform = "mattermost";
	readonly credentialKind = "mattermost-token";

	private siteURL: string | undefined;

	constructor(private readonly baseURL: string) {}

	// baseURL reaches Mattermost from this host and is usually loopback; the
	// address someone else's browser can reach is the one Mattermost publishes.
	private async browserBaseURL(): Promise<string> {
		if (this.siteURL) return this.siteURL;
		const response = await fetch(`${this.baseURL}/api/v4/config/client?format=old`);
		if (!response.ok) return this.baseURL;
		const published = (await response.json()) as { SiteURL?: string };
		const siteURL = trimTrailingSlash(published.SiteURL ?? "");
		if (!siteURL) return this.baseURL;
		this.siteURL = siteURL;
		return siteURL;
	}

	credentialRequirement(): CredentialRequirement {
		return {
			kind: "sign-in",
			credentialKind: this.credentialKind,
			fields: [
				{ name: "loginID", label: "Email or username", isSecret: false },
				{ name: "password", label: "Password", isSecret: true },
			],
		};
	}

	async issueCredential(answers: Record<string, string>): Promise<IssuedCredential> {
		const session = await this.signIn(answers.loginID ?? "", answers.password ?? "");
		const durable = await this.mintOwnAccessToken(session);
		const credential = { kind: this.credentialKind, secret: durable };
		return { credential, identity: await this.identity(credential) };
	}

	private async signIn(loginID: string, password: string): Promise<MattermostSession> {
		const response = await fetch(`${this.baseURL}/api/v4/users/login`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ login_id: loginID, password }),
		});
		if (response.status === 401 || response.status === 403) {
			throw new CredentialRefused("the messenger did not accept that sign-in");
		}
		if (!response.ok) {
			throw new Error(`mattermost login returned ${response.status}`);
		}
		const token = response.headers.get("token");
		const account = (await response.json()) as { id?: string };
		if (!token || !account.id) throw new Error("mattermost signed in without returning a session");
		return { token, userID: account.id };
	}

	private async mintOwnAccessToken(session: MattermostSession): Promise<string> {
		const response = await fetch(`${this.baseURL}/api/v4/users/${session.userID}/tokens`, {
			method: "POST",
			headers: { Authorization: `Bearer ${session.token}`, "Content-Type": "application/json" },
			body: JSON.stringify({ description: "internkim" }),
		});
		if (response.status === 403) {
			throw new CredentialRefused(
				"this messenger does not let a person mint their own access token; an administrator has to enable personal access tokens",
			);
		}
		const minted = (await response.json().catch(() => null)) as { token?: string } | null;
		if (!response.ok || !minted?.token) {
			throw new Error(`mattermost refused an access token (${response.status})`);
		}
		return minted.token;
	}

	async identity(actor: ActorCredential): Promise<PersonalIdentity> {
		const user = await this.ask<MattermostUser>(actor, "GET", "/users/me");
		return { externalID: user.id, name: displayNameOf(user), serverURL: this.baseURL };
	}

	async listConversations(actor: ActorCredential): Promise<PersonalConversation[]> {
		const teams = await this.ask<MattermostTeam[]>(actor, "GET", "/users/me/teams");
		const browserBaseURL = await this.browserBaseURL();
		const conversations: PersonalConversation[] = [];
		for (const team of teams) {
			const channels = await this.ask<MattermostChannel[]>(
				actor,
				"GET",
				`/users/me/teams/${team.id}/channels`,
			);
			conversations.push(
				...channels.map((channel) => asConversation(channel, webURLOf(browserBaseURL, team, channel))),
			);
		}
		return (await this.namedAfterTheOtherPerson(actor, conversations)).sort(directLast);
	}

	// A direct channel carries no name of its own: Mattermost puts the two
	// people's ids there, joined by a double underscore. Anyone showing the
	// conversation would otherwise have to know that and resolve it, so the
	// person on the other side is named here, once, for every client.
	private async namedAfterTheOtherPerson(
		actor: ActorCredential,
		conversations: PersonalConversation[],
	): Promise<PersonalConversation[]> {
		const me = await this.identity(actor);
		const counterparts = new Set<string>();
		for (const conversation of conversations) {
			for (const externalID of conversation.participantExternalIDs ?? []) {
				if (externalID !== me.externalID) counterparts.add(externalID);
			}
		}
		if (counterparts.size === 0) return conversations;

		const users = await this.ask<MattermostUser[]>(actor, "POST", "/users/ids", [...counterparts]);
		const nameOf = new Map(users.map((user) => [user.id, displayNameOf(user)]));
		return conversations.map((conversation) => {
			const others = (conversation.participantExternalIDs ?? [])
				.filter((externalID) => externalID !== me.externalID)
				.map((externalID) => nameOf.get(externalID))
				.filter((name): name is string => Boolean(name));
			return others.length > 0 ? { ...conversation, name: others.join(", ") } : conversation;
		});
	}

	async listPeople(actor: ActorCredential): Promise<PersonalPerson[]> {
		const users = await this.ask<MattermostUser[]>(actor, "GET", "/users?per_page=200&active=true");
		return users.map((user) => ({ externalID: user.id, name: displayNameOf(user) }));
	}

	async ensureDirectConversation(
		actor: ActorCredential,
		counterpartExternalIDs: string[],
	): Promise<PersonalConversation> {
		const me = await this.identity(actor);
		const everyone = [...new Set([me.externalID, ...counterpartExternalIDs])];
		const path = everyone.length === 2 ? "/channels/direct" : "/channels/group";
		const channel = await this.ask<MattermostChannel>(actor, "POST", path, everyone);
		const [team] = await this.ask<MattermostTeam[]>(actor, "GET", "/users/me/teams");
		return {
			id: channel.id,
			name: channel.display_name || channel.name,
			kind: "dm",
			isPrivate: true,
			webURL: team ? webURLOf(await this.browserBaseURL(), team, channel) : undefined,
		};
	}

	async listMessages(
		actor: ActorCredential,
		conversationID: string,
		before?: string,
	): Promise<PersonalMessagePage> {
		const query = before ? `&before=${encodeURIComponent(before)}` : "";
		const page = await this.ask<{ order: string[]; posts: Record<string, MattermostPost> }>(
			actor,
			"GET",
			`/channels/${conversationID}/posts?per_page=${pageSize}${query}`,
		);
		const oldestFirst = page.order
			.map((id) => page.posts[id])
			.filter((post): post is MattermostPost => post !== undefined)
			.reverse()
			.map(asMessage);
		const { kept, hasOlder } = withinBudget(oldestFirst);
		return { messages: kept, hasMoreBefore: hasOlder || page.order.length >= pageSize };
	}

	async sendMessage(
		actor: ActorCredential,
		conversationID: string,
		body: string,
		parentID?: string,
		attachments: OutgoingAttachment[] = [],
	): Promise<PersonalMessage> {
		const fileIDs = await this.uploadAll(actor, conversationID, attachments);
		const post = await this.ask<MattermostPost>(actor, "POST", "/posts", {
			channel_id: conversationID,
			message: body,
			root_id: parentID ?? "",
			file_ids: fileIDs,
		});
		return asMessage(post);
	}

	private async uploadAll(
		actor: ActorCredential,
		conversationID: string,
		attachments: OutgoingAttachment[],
	): Promise<string[]> {
		const fileIDs: string[] = [];
		for (const attachment of attachments) {
			fileIDs.push(await this.upload(actor, conversationID, attachment));
		}
		return fileIDs;
	}

	private async upload(
		actor: ActorCredential,
		conversationID: string,
		attachment: OutgoingAttachment,
	): Promise<string> {
		requireMatchingCredential(this, actor);
		if (isAlreadyKept(attachment)) {
			throw new UnsupportedByPlatform(this.platform, "post a file it has not been handed");
		}
		const form = new FormData();
		form.append("channel_id", conversationID);
		form.append(
			"files",
			new Blob([Buffer.from(attachment.contentBase64, "base64")], { type: attachment.contentType }),
			attachment.filename,
		);
		const response = await fetch(`${this.baseURL}/api/v4/files`, {
			method: "POST",
			headers: { Authorization: `Bearer ${actor.secret}` },
			body: form,
		});
		if (!response.ok) {
			throw new Error(`mattermost refused ${attachment.filename} (${response.status})`);
		}
		const uploaded = (await response.json()) as { file_infos?: { id?: string }[] };
		const fileID = uploaded.file_infos?.[0]?.id;
		if (!fileID) throw new Error(`mattermost stored ${attachment.filename} without naming it`);
		return fileID;
	}

	async readAttachment(
		actor: ActorCredential,
		attachmentID: string,
		largestBytes: number,
	): Promise<PersonalFile | null> {
		const described = await this.readAsPerson(actor, `/files/${encodeURIComponent(attachmentID)}/info`);
		if (!described) return null;
		const info = (await described.json()) as MattermostFileInfo;
		if (info.size > largestBytes) return null;
		const response = await this.readAsPerson(actor, `/files/${encodeURIComponent(attachmentID)}`);
		if (!response) return null;
		const bytes = new Uint8Array(await response.arrayBuffer());
		if (bytes.length === 0 || bytes.length > largestBytes) return null;
		return {
			filename: info.name,
			contentType: info.mime_type || mediaTypeOfName(info.extension || info.name),
			contentBase64: Buffer.from(bytes).toString("base64"),
		};
	}

	async editMessage(
		actor: ActorCredential,
		_conversationID: string,
		messageID: string,
		body: string,
	): Promise<PersonalMessage> {
		const post = await this.ask<MattermostPost>(actor, "PUT", `/posts/${messageID}/patch`, {
			message: body,
		});
		return asMessage(post);
	}

	async deleteMessage(
		actor: ActorCredential,
		_conversationID: string,
		messageID: string,
	): Promise<void> {
		await this.ask<void>(actor, "DELETE", `/posts/${messageID}`);
	}

	async addReaction(
		actor: ActorCredential,
		_conversationID: string,
		messageID: string,
		emoji: string,
	): Promise<void> {
		const me = await this.identity(actor);
		await this.ask<void>(actor, "POST", "/reactions", {
			user_id: me.externalID,
			post_id: messageID,
			emoji_name: emoji,
		});
	}

	async removeReaction(
		actor: ActorCredential,
		_conversationID: string,
		messageID: string,
		emoji: string,
	): Promise<void> {
		const me = await this.identity(actor);
		await this.ask<void>(
			actor,
			"DELETE",
			`/users/${me.externalID}/posts/${messageID}/reactions/${encodeURIComponent(emoji)}`,
		);
	}

	async listCustomEmoji(actor: ActorCredential): Promise<PersonalEmoji[]> {
		const listed = await this.ask<{ name: string }[]>(actor, "GET", "/emoji?per_page=200");
		return listed.map((emoji) => ({ name: emoji.name }));
	}

	async readCustomEmojiImage(
		actor: ActorCredential,
		name: string,
		largestBytes: number,
	): Promise<PersonalImage | null> {
		const emojiID = await this.emojiIDOf(actor, name);
		if (!emojiID) return null;
		return this.readImage(actor, `/emoji/${encodeURIComponent(emojiID)}/image`, largestBytes);
	}

	private async emojiIDOf(actor: ActorCredential, name: string): Promise<string | null> {
		const response = await this.readAsPerson(actor, `/emoji/name/${encodeURIComponent(name)}`);
		if (!response) return null;
		const emoji = (await response.json()) as { id?: string };
		return emoji.id ?? null;
	}

	async readProfilePicture(
		actor: ActorCredential,
		externalID: string,
		largestBytes: number,
	): Promise<PersonalImage | null> {
		return this.readImage(actor, `/users/${encodeURIComponent(externalID)}/image`, largestBytes);
	}

	private async readAsPerson(actor: ActorCredential, path: string): Promise<Response | null> {
		requireMatchingCredential(this, actor);
		const response = await fetch(`${this.baseURL}/api/v4${path}`, {
			headers: { Authorization: `Bearer ${actor.secret}` },
		});
		return response.ok ? response : null;
	}

	private async readImage(
		actor: ActorCredential,
		path: string,
		largestBytes: number,
	): Promise<PersonalImage | null> {
		const response = await this.readAsPerson(actor, path);
		if (!response) return null;
		const type = response.headers.get("content-type") ?? "image/png";
		const bytes = new Uint8Array(await response.arrayBuffer());
		if (bytes.length === 0 || bytes.length > largestBytes) return null;
		return { dataURL: `data:${type};base64,${Buffer.from(bytes).toString("base64")}` };
	}

	private async ask<Value>(
		actor: ActorCredential,
		method: string,
		path: string,
		body?: unknown,
	): Promise<Value> {
		requireMatchingCredential(this, actor);
		const response = await fetch(`${this.baseURL}/api/v4${path}`, {
			method,
			headers: {
				Authorization: `Bearer ${actor.secret}`,
				...(body === undefined ? {} : { "Content-Type": "application/json" }),
			},
			body: body === undefined ? undefined : JSON.stringify(body),
		});
		if (!response.ok) {
			throw new Error(`mattermost ${method} ${path} returned ${response.status}`);
		}
		if (response.status === 204) return undefined as Value;
		return (await response.json()) as Value;
	}
}

function displayNameOf(user: MattermostUser): string {
	return [user.first_name, user.last_name].filter(Boolean).join(" ") || user.username;
}

function directParticipantsOf(channel: MattermostChannel): string[] | undefined {
	if (channel.type !== "D") return undefined;
	const everyone = channel.name.split("__").filter(Boolean);
	return everyone.length === 2 ? everyone : undefined;
}

function webURLOf(browserBaseURL: string, team: MattermostTeam, channel: MattermostChannel): string {
	return `${browserBaseURL}/${encodeURIComponent(team.name)}/channels/${encodeURIComponent(channel.name)}`;
}

function trimTrailingSlash(value: string): string {
	return value.trim().replace(/\/+$/, "");
}

function asConversation(channel: MattermostChannel, webURL: string): PersonalConversation {
	return {
		id: channel.id,
		name: channel.display_name || channel.name,
		kind: channel.type === "D" || channel.type === "G" ? "dm" : "group",
		isPrivate: channel.type !== "O",
		participantExternalIDs: directParticipantsOf(channel),
		webURL,
	};
}

function directLast(left: PersonalConversation, right: PersonalConversation): number {
	if (left.kind !== right.kind) return left.kind === "dm" ? 1 : -1;
	return left.name < right.name ? -1 : left.name > right.name ? 1 : 0;
}

function asMessage(post: MattermostPost): PersonalMessage {
	return {
		id: post.id,
		conversationID: post.channel_id,
		parentID: post.root_id || undefined,
		authorExternalID: post.user_id,
		body: post.message,
		postedAt: new Date(post.create_at).toISOString(),
		editedAt: post.edit_at ? new Date(post.edit_at).toISOString() : undefined,
		reactions: groupReactions(post.metadata?.reactions ?? []),
		attachments: (post.metadata?.files ?? []).map(asAttachment),
	};
}

function asAttachment(file: MattermostFileInfo): PersonalAttachment {
	return {
		id: file.id,
		filename: file.name,
		contentType: file.mime_type || mediaTypeOfName(file.extension || file.name),
		sizeBytes: file.size,
		digest: "",
		...(file.width && file.height
			? { widthPixels: file.width, heightPixels: file.height }
			: {}),
	};
}

// Mattermost stores a file with no media type when whatever uploaded it sent
// none, and a reader shown "no type" draws a document card for a photograph.
// The name still says what it is: this is the file extension's own vocabulary,
// not a guess about the contents.
const mediaTypeOfExtension: Record<string, string> = {
	png: "image/png",
	jpg: "image/jpeg",
	jpeg: "image/jpeg",
	gif: "image/gif",
	webp: "image/webp",
	heic: "image/heic",
	heif: "image/heif",
	bmp: "image/bmp",
	svg: "image/svg+xml",
	pdf: "application/pdf",
	mp4: "video/mp4",
	mov: "video/quicktime",
	webm: "video/webm",
	mp3: "audio/mpeg",
	wav: "audio/wav",
	m4a: "audio/mp4",
};

function mediaTypeOfName(nameOrExtension: string): string {
	const extension = nameOrExtension.split(".").pop()?.toLowerCase() ?? "";
	return mediaTypeOfExtension[extension] ?? "";
}

function groupReactions(reactions: MattermostReaction[]): PersonalReaction[] {
	const byEmoji = new Map<string, string[]>();
	for (const reaction of reactions) {
		const already = byEmoji.get(reaction.emoji_name) ?? [];
		already.push(reaction.user_id);
		byEmoji.set(reaction.emoji_name, already);
	}
	return [...byEmoji].map(([emoji, byExternalIDs]) => ({ emoji, byExternalIDs }));
}
