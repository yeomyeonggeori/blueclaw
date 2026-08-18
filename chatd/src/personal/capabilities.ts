import type { PersonalGateway } from "./gateway.ts";
import {
	MalformedRequest,
	parseCredentialAnswers,
	parsePersonRequest,
	requireConversation,
	requireExternalID,
	requireLargestBytes,
	requireMessage,
	requireName,
} from "./parse.ts";

// The agent's own account on the messenger, when the platform knows it. A
// direct conversation with the agent is a conversation with the product, and
// only the product knows which account that is.
export type PersonCapability = (
	gateway: PersonalGateway,
	requestBody: unknown,
	agentExternalID?: string,
) => Promise<object>;

export const personCapabilities: Record<string, PersonCapability> = {
	"person.credential.requirement": async (gateway) => gateway.credentialRequirement(),
	"person.credential.issue": async (gateway, body) =>
		await gateway.issueCredential(parseCredentialAnswers(body)),
	"person.identity": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.identity(request.actor);
	},
	"person.conversations.list": async (gateway, body, agentExternalID) => {
		const request = parsePersonRequest(body);
		const conversations = await gateway.listConversations(request.actor);
		return {
			conversations: conversations.map((conversation) => ({
				...conversation,
				isWithTheAgent: Boolean(
					agentExternalID && (conversation.participantExternalIDs ?? []).includes(agentExternalID),
				),
			})),
		};
	},
	"person.people.list": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return { people: await gateway.listPeople(request.actor) };
	},
	"person.dm.ensure": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.ensureDirectConversation(request.actor, request.counterpartExternalIDs);
	},
	"person.messages.list": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.listMessages(request.actor, requireConversation(request), request.before);
	},
	"person.message.send": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.sendMessage(
			request.actor,
			requireConversation(request),
			request.body ?? "",
			request.parentID,
			request.attachments,
		);
	},
	"person.message.edit": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.editMessage(
			request.actor,
			requireConversation(request),
			requireMessage(request),
			request.body ?? "",
		);
	},
	"person.message.delete": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.deleteMessage(request.actor, requireConversation(request), requireMessage(request));
		return {};
	},
	"person.reaction.add": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.addReaction(
			request.actor,
			requireConversation(request),
			requireMessage(request),
			requireEmoji(request.emoji),
		);
		return {};
	},
	"person.reaction.remove": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.removeReaction(
			request.actor,
			requireConversation(request),
			requireMessage(request),
			requireEmoji(request.emoji),
		);
		return {};
	},
	"person.emoji.list": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return { emoji: await gateway.listCustomEmoji(request.actor) };
	},
	"person.emoji.image": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return {
			image: await gateway.readCustomEmojiImage(
				request.actor,
				requireName(request),
				requireLargestBytes(request),
			),
		};
	},
	"person.picture": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return {
			image: await gateway.readProfilePicture(
				request.actor,
				requireExternalID(request),
				requireLargestBytes(request),
			),
		};
	},
	"person.message.attachment": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return {
			file: await gateway.readAttachment(
				request.actor,
				requireMessage(request),
				requireLargestBytes(request),
			),
		};
	},
};

function requireEmoji(emoji: string | undefined): string {
	if (!emoji) throw new MalformedRequest("missing required field emoji");
	return emoji;
}
