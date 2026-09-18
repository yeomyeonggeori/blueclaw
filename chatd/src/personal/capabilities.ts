import type { PersonalGateway } from "./gateway.ts";
import {
	MalformedRequest,
	parseCredentialAnswers,
	parseMemberExternalIDs,
	parseNewChannel,
	parsePersonRequest,
	requireConversation,
	requireExternalID,
	requireLargestBytes,
	requireMessage,
	requireLoopbackArrivalsURL,
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
			agentExternalID,
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
	"person.channel.create": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.createChannel(request.actor, parseNewChannel(body));
	},
	"person.channels.open.list": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return { channels: await gateway.listOpenChannels(request.actor) };
	},
	"person.channel.join": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.joinChannel(request.actor, requireConversation(request));
		return {};
	},
	"person.channel.members.add": async (gateway, body) => {
		const request = parsePersonRequest(body);
		const members = parseMemberExternalIDs(body);
		if (members.length === 0) throw new MalformedRequest("memberExternalIDs must name someone to add");
		return await gateway.addChannelMembers(request.actor, requireConversation(request), members);
	},
	"person.channel.leave": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.leaveChannel(request.actor, requireConversation(request));
		return {};
	},
	"person.channel.owner.set": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.handOverChannel(request.actor, requireConversation(request), requireExternalID(request));
		return {};
	},
	"person.channel.delete": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.deleteChannel(request.actor, requireConversation(request));
		return {};
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
	"person.arrivals.watch": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.watchArrivals(request.actor, requireLoopbackArrivalsURL(request));
		return {};
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
