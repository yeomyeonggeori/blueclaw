import {
	requireMatchingCredential,
	type ActorCredential,
	type PersonalConversation,
	type PersonalGateway,
	type PersonalIdentity,
	type PersonalMessage,
	type PersonalMessagePage,
	type PersonalPerson,
	type PersonalReaction,
} from "./gateway.ts";
import { withinBudget } from "./page-budget.ts";

type MattermostChannel = { id: string; display_name: string; name: string; type: string };
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
	metadata?: { reactions?: MattermostReaction[] };
};

const pageSize = 50;

export function createMattermostPersonalGateway(baseURL: string): PersonalGateway {
	return new MattermostPersonalGateway(baseURL);
}

class MattermostPersonalGateway implements PersonalGateway {
	readonly platform = "mattermost";
	readonly credentialKind = "mattermost-token";

	constructor(private readonly baseURL: string) {}

	async identity(actor: ActorCredential): Promise<PersonalIdentity> {
		const user = await this.ask<MattermostUser>(actor, "GET", "/users/me");
		return { externalID: user.id, name: displayNameOf(user) };
	}

	async listConversations(actor: ActorCredential): Promise<PersonalConversation[]> {
		const teams = await this.ask<{ id: string }[]>(actor, "GET", "/users/me/teams");
		const channels: MattermostChannel[] = [];
		for (const team of teams) {
			channels.push(
				...(await this.ask<MattermostChannel[]>(actor, "GET", `/users/me/teams/${team.id}/channels`)),
			);
		}
		return channels.map(asConversation).sort(directLast);
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
		return { id: channel.id, name: channel.display_name || channel.name, kind: "dm" };
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
	): Promise<PersonalMessage> {
		const post = await this.ask<MattermostPost>(actor, "POST", "/posts", {
			channel_id: conversationID,
			message: body,
			root_id: parentID ?? "",
		});
		return asMessage(post);
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

function asConversation(channel: MattermostChannel): PersonalConversation {
	return {
		id: channel.id,
		name: channel.display_name || channel.name,
		kind: channel.type === "D" || channel.type === "G" ? "dm" : "group",
		participantExternalIDs: directParticipantsOf(channel),
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
	};
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
