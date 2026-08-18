import type { AdapterPostableMessage, CardElement, FileUpload } from "chat";
import { BuzzAdapter } from "./adapters/buzz/adapter.ts";
import type { MattermostAdapter } from "./adapters/mattermost/adapter.ts";
import type { ChatdConfiguration } from "./configuration.ts";
import { importAttachmentToDirectory } from "./outbound-attachments.ts";
import { supportsChannelProvisioning } from "./channels.ts";
import {
	ensureUserDirectMessageChannel,
	listUserConversations,
	pubkeyFromSecret,
	sendChannelMessageAsUser,
	sendDirectMessageAsUser,
} from "./adapters/buzz/user-session.ts";
import { encodeHistoryCursor } from "./visible-context.ts";
import { buildVisibleContext, decodeHistoryCursor } from "./visible-context.ts";
import {
	parseAttachmentImportRequest,
	parseChannelEnsureRequest,
	parseDirectMessageEnsureRequest,
	parseConversationsListRequest,
	parsePeopleListRequest,
	parseDirectMessageSendRequest,
	parseHistoryFetchRequest,
	parseIdentityResolveRequest,
	parseInteractionResolveRequest,
	parseMessageDeleteRequest,
	parseMessageEditRequest,
	parseProgressRequest,
	parseReactionRequest,
	parseReplySendRequest,
} from "./outbound-parse.ts";
import { personCapabilities, type PersonCapability } from "./personal/capabilities.ts";
import { MalformedRequest } from "./personal/parse.ts";
import { CredentialRefused, type PersonalGateway } from "./personal/gateway.ts";
import { AttachmentRefused } from "./outgoing-attachment.ts";
import type {
	AgentPartDocument,
	AttachmentImportResponse,
	ChannelEnsureResponse,
	DirectMessageEnsureResponse,
	ConversationsListResponse,
	PeopleListResponse,
	DirectMessageSendResponse,
	HistoryFetchResponse,
	IdentityResolveResponse,
	InputAttachmentDocument,
	ReplyAttachmentDocument,
	ReplySendRequest,
	ReplySendResponse,
} from "./outbound-types.ts";

type PlatformChatAdapter = MattermostAdapter | BuzzAdapter;

type CapabilityHandler = (
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestDocument: unknown,
) => Promise<object>;

const OUTBOUND_ROUTE_PATTERN = /^\/v1\/platform\/([^/]+)\/([^/]+)$/;

const capabilityHandlers: Record<string, CapabilityHandler> = {
	"reply.send": handleReplySend,
	"progress.start": handleProgressStart,
	"progress.stop": handleProgressStop,
	"reaction.add": handleReactionAdd,
	"reaction.remove": handleReactionRemove,
	"history.fetch": handleHistoryFetch,
	"interaction.resolve": handleInteractionResolve,
	"attachments.import": handleAttachmentsImport,
	"identity.resolve": handleIdentityResolve,
	"channel.ensure": handleChannelEnsure,
	"dm.ensure": handleDirectMessageEnsure,
	"dm.send": handleDirectMessageSend,
	"conversations.list": handleConversationsList,
	"people.list": handlePeopleList,
	"message.edit": handleMessageEdit,
	"message_delete": handleMessageDelete,
};

export function createOutboundHandler(
	adapters: Record<string, PlatformChatAdapter>,
	configuration: ChatdConfiguration,
	personalGateways: Record<string, PersonalGateway> = {},
): (request: Request) => Promise<Response> {
	return async function handleOutboundRequest(request: Request): Promise<Response> {
		if (request.method !== "POST") {
			return jsonResponse(405, { error: "method not allowed" });
		}

		const routeMatch = OUTBOUND_ROUTE_PATTERN.exec(new URL(request.url).pathname);
		if (!routeMatch) {
			return jsonResponse(404, { error: "not found" });
		}

		const [, platform, capabilityName] = routeMatch;
		if (!platform || !capabilityName) {
			return jsonResponse(404, { error: "not found" });
		}
		const adapter = adapters[platform];
		if (!adapter) {
			return jsonResponse(404, { error: `unknown platform ${platform}` });
		}

		const personCapability = personCapabilities[capabilityName];
		const handler = capabilityHandlers[capabilityName];
		if (!personCapability && !handler) {
			return jsonResponse(404, { error: `unknown capability ${capabilityName}` });
		}

		let requestDocument: unknown;
		try {
			requestDocument = await request.json();
		} catch {
			return jsonResponse(400, { error: "expected a JSON body" });
		}

		if (personCapability) {
			return answerAsPerson(personCapability, personalGateways[platform], platform, requestDocument, agentExternalIDOf(adapter));
		}

		try {
			const responseDocument = await handler?.(adapter, configuration, requestDocument);
			return jsonResponse(200, responseDocument ?? {});
		} catch (error) {
			return jsonResponse(502, { error: error instanceof Error ? error.message : String(error) });
		}
	};
}

function agentExternalIDOf(adapter: PlatformChatAdapter): string | undefined {
	if ("botUserId" in adapter && typeof adapter.botUserId === "string") return adapter.botUserId;
	if ("botPubkey" in adapter && typeof adapter.botPubkey === "string") return adapter.botPubkey;
	return undefined;
}

async function answerAsPerson(
	capability: PersonCapability,
	gateway: PersonalGateway | undefined,
	platform: string,
	requestDocument: unknown,
	agentExternalID?: string,
): Promise<Response> {
	if (!gateway) {
		return jsonResponse(404, { error: `${platform} cannot act as a person` });
	}
	try {
		return jsonResponse(200, await capability(gateway, requestDocument, agentExternalID));
	} catch (error) {
		if (error instanceof MalformedRequest) {
			return jsonResponse(400, { error: error.message });
		}
		if (error instanceof CredentialRefused) {
			return jsonResponse(401, { error: error.message });
		}
		if (error instanceof AttachmentRefused) {
			return jsonResponse(415, { error: error.message, refusedAttachments: error.refusals });
		}
		return jsonResponse(502, { error: error instanceof Error ? error.message : String(error) });
	}
}

function jsonResponse(statusCode: number, body: object): Response {
	return new Response(JSON.stringify(body), {
		status: statusCode,
		headers: { "Content-Type": "application/json" },
	});
}

async function handleReplySend(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<ReplySendResponse> {
	const requestDocument = parseReplySendRequest(requestBody);
	const fileUploads = await buildFileUploads(requestDocument.attachments ?? []);
	const message = buildPostableMessage(requestDocument, fileUploads);
	const result =
		requestDocument.isError && adapter instanceof BuzzAdapter
			? await adapter.postMessage(requestDocument.replyTargetID, message, [["reply-kind", "error"]])
			: await adapter.postMessage(requestDocument.replyTargetID, message);
	return { dispatchID: result.id };
}

function buildPostableMessage(
	requestDocument: ReplySendRequest,
	fileUploads: FileUpload[],
): AdapterPostableMessage {
	const interactionOptions = requestDocument.interaction?.options ?? [];
	if (interactionOptions.length > 0) {
		return { card: buildInteractionCard(requestDocument), files: fileUploads };
	}
	if (fileUploads.length > 0) {
		return { markdown: requestDocument.message, files: fileUploads };
	}
	return requestDocument.message;
}

function buildInteractionCard(requestDocument: ReplySendRequest): CardElement {
	const interaction = requestDocument.interaction;
	const options = interaction?.options ?? [];
	const introductionText = requestDocument.message.trim();
	const children: CardElement["children"] = [];
	if (introductionText) {
		children.push({ type: "text", content: introductionText });
	}
	children.push({
		type: "actions",
		children: options.map((option) => ({
			type: "button",
			id: option.key,
			label: option.label,
			value: option.value,
		})),
	});
	return {
		type: "card",
		title: interaction?.question || interaction?.message || undefined,
		children,
	};
}

async function buildFileUploads(attachments: ReplyAttachmentDocument[]): Promise<FileUpload[]> {
	const fileUploads: FileUpload[] = [];
	for (const attachment of attachments) {
		const fileBytes = await readAttachmentBytes(attachment);
		if (!fileBytes) {
			continue;
		}
		fileUploads.push({
			data: fileBytes,
			filename: attachment.filename?.trim() || "attachment",
			mimeType: attachment.contentType,
		});
	}
	return fileUploads;
}

async function readAttachmentBytes(attachment: ReplyAttachmentDocument): Promise<Buffer | null> {
	if (attachment.contentBase64) {
		return Buffer.from(attachment.contentBase64, "base64");
	}
	if (attachment.devicePath) {
		const file = Bun.file(attachment.devicePath);
		if (await file.exists()) {
			return Buffer.from(await file.arrayBuffer());
		}
	}
	return null;
}

async function handleProgressStart(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseProgressRequest(requestBody);
	await adapter.startTyping(requestDocument.replyTargetID);
	return {};
}

async function handleProgressStop(): Promise<Record<string, never>> {
	return {};
}

async function handleReactionAdd(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseReactionRequest(requestBody);
	await adapter.addReaction("", requestDocument.messageID, requestDocument.emojiName);
	return {};
}

async function handleReactionRemove(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseReactionRequest(requestBody);
	await adapter.removeReaction("", requestDocument.messageID, requestDocument.emojiName);
	return {};
}

function requireBuzzAdapterForDirectMessages(adapter: PlatformChatAdapter): BuzzAdapter {
	if (!(adapter instanceof BuzzAdapter)) {
		throw new Error(`platform ${adapter.name} does not support user direct messages`);
	}
	return adapter;
}

async function handleDirectMessageEnsure(
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<DirectMessageEnsureResponse> {
	const requestDocument = parseDirectMessageEnsureRequest(requestBody);
	const buzzAdapter = requireBuzzAdapterForDirectMessages(adapter);
	if (requestDocument.channelId) {
		const replyTargetID = buzzAdapter.encodeThreadId({ channelId: requestDocument.channelId });
		return {
			channelID: requestDocument.channelId,
			replyTargetID,
			historyCursor: encodeHistoryCursor({ threadId: replyTargetID }),
			userPubkeyHex: pubkeyFromSecret(requestDocument.userSecretHex),
		};
	}
	const channel = await ensureUserDirectMessageChannel(
		requireBuzzRelayURL(configuration),
		requestDocument.userSecretHex,
		requestDocument.counterpartPubkeyHex ?? buzzAdapter.botPubkey,
	);
	const replyTargetID = buzzAdapter.encodeThreadId({ channelId: channel.channelID });
	const botUser = await buzzAdapter.getUser(buzzAdapter.botPubkey).catch(() => null);
	return {
		channelID: channel.channelID,
		replyTargetID,
		historyCursor: encodeHistoryCursor({ threadId: replyTargetID }),
		userPubkeyHex: channel.userPubkeyHex,
		botName: botUser?.fullName,
		botAvatarURL: botUser?.avatarUrl,
	};
}

async function handleConversationsList(
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<ConversationsListResponse> {
	const requestDocument = parseConversationsListRequest(requestBody);
	requireBuzzAdapterForDirectMessages(adapter);
	const conversations = await listUserConversations(
		requireBuzzRelayURL(configuration),
		requestDocument.userSecretHex,
	);
	return {
		conversations: conversations.map((conversation) => ({
			id: conversation.channelID,
			name: conversation.name,
			kind: conversation.isDM ? "dm" : "group",
			participantExternalIDs: conversation.participantPubkeyHexes,
		})),
	};
}

async function handlePeopleList(
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<PeopleListResponse> {
	const requestDocument = parsePeopleListRequest(requestBody);
	const buzzAdapter = requireBuzzAdapterForDirectMessages(adapter);
	const people = await buzzAdapter.listPeople(pubkeyFromSecret(requestDocument.userSecretHex));
	return { people };
}

async function handleDirectMessageSend(
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<DirectMessageSendResponse> {
	const requestDocument = parseDirectMessageSendRequest(requestBody);
	const buzzAdapter = requireBuzzAdapterForDirectMessages(adapter);
	const relayURL = requireBuzzRelayURL(configuration);
	const attachments = (requestDocument.attachments ?? []).map((attachment) => ({
		contentBase64: attachment.contentBase64 ?? "",
		filename: attachment.filename ?? "",
		contentType: attachment.contentType ?? "application/octet-stream",
	}));
	if (requestDocument.channelId) {
		const sent = await sendChannelMessageAsUser({
			relayURL,
			userSecretHex: requestDocument.userSecretHex,
			channelID: requestDocument.channelId,
			message: requestDocument.message,
			attachments,
			replyToRootId: requestDocument.replyToRootId,
		});
		return {
			channelID: requestDocument.channelId,
			replyTargetID: buzzAdapter.encodeThreadId({ channelId: requestDocument.channelId }),
			messageID: sent.id,
		};
	}
	const channel = await ensureUserDirectMessageChannel(
		relayURL,
		requestDocument.userSecretHex,
		buzzAdapter.botPubkey,
	);
	const messageID = await sendDirectMessageAsUser({
		relayURL,
		userSecretHex: requestDocument.userSecretHex,
		counterpartPubkeyHex: buzzAdapter.botPubkey,
		message: requestDocument.message,
		attachments,
	});
	return {
		channelID: channel.channelID,
		replyTargetID: buzzAdapter.encodeThreadId({ channelId: channel.channelID }),
		messageID,
	};
}

function requireBuzzRelayURL(configuration: ChatdConfiguration): string {
	const relayURL = configuration.buzz?.relayURL;
	if (!relayURL) throw new Error("buzz relay is not configured");
	return relayURL;
}

async function handleChannelEnsure(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<ChannelEnsureResponse> {
	const requestDocument = parseChannelEnsureRequest(requestBody);
	if (!supportsChannelProvisioning(adapter)) {
		throw new Error(`platform ${adapter.name} cannot provision channels`);
	}
	return await adapter.ensureChannel(requestDocument);
}

async function handleMessageEdit(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<ReplySendResponse> {
	const requestDocument = parseMessageEditRequest(requestBody);
	const result = await adapter.editMessage(
		requestDocument.replyTargetID,
		requestDocument.messageID,
		requestDocument.message,
	);
	return { dispatchID: result.id };
}

async function handleMessageDelete(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseMessageDeleteRequest(requestBody);
	await adapter.deleteMessage(requestDocument.replyTargetID, requestDocument.messageID);
	return {};
}

async function handleHistoryFetch(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<HistoryFetchResponse> {
	const requestDocument = parseHistoryFetchRequest(requestBody);
	const { threadId, cursor } = decodeHistoryCursor(requestDocument.historyCursor);
	const context = await buildVisibleContext(adapter, threadId, { cursor, limit: requestDocument.limit });
	return {
		messages: context.messages,
		hasMoreBefore: context.hasMoreBefore,
		historyCursor: context.historyCursor,
		channelID: context.channelID,
		channelName: context.channelName,
		conversationType: context.conversationType,
	};
}

async function handleInteractionResolve(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseInteractionResolveRequest(requestBody);
	if (adapter instanceof BuzzAdapter || !adapter.fetchMessage) {
		return {};
	}
	const existingMessage = await adapter.fetchMessage("", requestDocument.dispatchID);
	if (!existingMessage) {
		return {};
	}

	const threadId = adapter.encodeThreadId({
		channelId: existingMessage.raw.channel_id,
		rootPostId: existingMessage.raw.root_id || undefined,
	});
	const frozenCard: CardElement = {
		type: "card",
		children: existingMessage.text ? [{ type: "text", content: existingMessage.text }] : [],
	};
	await adapter.editMessage(threadId, requestDocument.dispatchID, { card: frozenCard });
	return {};
}

async function handleAttachmentsImport(
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<AttachmentImportResponse> {
	const requestDocument = parseAttachmentImportRequest(requestBody);
	if (adapter instanceof BuzzAdapter) {
		return { inputParts: [], inputAttachments: [] };
	}
	const importedAttachments = await Promise.all(
		requestDocument.inputAttachments.map((attachment) =>
			importAttachmentToDirectory(configuration, requestDocument.targetDirectoryPath, attachment),
		),
	);

	return {
		inputAttachments: importedAttachments,
		inputParts: importedAttachments
			.filter((attachment) => attachment.isAvailable && attachment.path)
			.map((attachment) => agentPartForAttachment(attachment, requestDocument.messageID)),
	};
}

function agentPartForAttachment(attachment: InputAttachmentDocument, messageID: string): AgentPartDocument {
	const isImage = (attachment.contentType ?? "").startsWith("image/");
	const source = { platform: "mattermost", messageID, fileID: attachment.fileID };
	if (isImage) {
		return {
			type: "image",
			image: { path: attachment.path ?? "", filename: attachment.filename, mimeType: attachment.contentType },
			source,
		};
	}
	return {
		type: "file",
		file: {
			path: attachment.path ?? "",
			filename: attachment.filename,
			contentType: attachment.contentType,
			sizeBytes: attachment.sizeBytes,
		},
		source,
	};
}

async function handleIdentityResolve(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<IdentityResolveResponse> {
	const requestDocument = parseIdentityResolveRequest(requestBody);
	if (!adapter.getUser) {
		return {};
	}
	const user = await adapter.getUser(requestDocument.senderID);
	if (!user) {
		return {};
	}
	return { displayName: user.fullName, email: user.email };
}
