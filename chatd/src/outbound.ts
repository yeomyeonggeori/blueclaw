import type { AdapterPostableMessage, FileUpload } from "chat";
import { BuzzAdapter } from "./adapters/buzz/adapter.ts";
import type { MattermostAdapter } from "./adapters/mattermost/adapter.ts";
import type { ChatdConfiguration } from "./configuration.ts";
import { fetchAttachmentForDirectory, supportsAttachmentFetching } from "./outbound-attachments.ts";
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
	parseMessageDeleteRequest,
	parseMessageEditRequest,
	parseMessagePostRequest,
	parseMessageSearchRequest,
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
	IdentitySelfResponse,
	InputAttachmentDocument,
	MessagePostRequest,
	MessagePostResponse,
	MessageSearchRequest,
	MessageSearchResponse,
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
	"attachments.import": handleAttachmentsImport,
	"identity.resolve": handleIdentityResolve,
	"identity.self": handleIdentitySelf,
	"channel.ensure": handleChannelEnsure,
	"dm.ensure": handleDirectMessageEnsure,
	"dm.send": handleDirectMessageSend,
	"conversations.list": handleConversationsList,
	"people.list": handlePeopleList,
	"message.edit": handleMessageEdit,
	"message.post": handleMessagePost,
	"message.search": handleMessageSearch,
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
			if (error instanceof MalformedRequest) {
				return jsonResponse(400, { error: error.message });
			}
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
	// The thread says where the reply belongs and the answered message says what it
	// is a reply to. Without the second, somebody who wrote deep in a thread finds
	// the answer at the top of it.
	const answeringTags = answeringTagsFor(adapter, requestDocument.answeringMessageID);
	if (requestDocument.replyKind === "progress" && adapter instanceof BuzzAdapter) {
		return {
			dispatchID: (await adapter.postMessage(requestDocument.replyTargetID, message, [["reply-kind", "progress"], ...answeringTags])).id
		};
	}
	const result =
		requestDocument.isError && adapter instanceof BuzzAdapter
			? await adapter.postMessage(requestDocument.replyTargetID, message, [["reply-kind", "error"], ...answeringTags])
			: await adapter.postMessage(requestDocument.replyTargetID, message, answeringTags);
	return { dispatchID: result.id };
}

function buildPostableMessage(
	requestDocument: ReplySendRequest,
	fileUploads: FileUpload[],
): AdapterPostableMessage {
	const markdown = messageCarryingItsOptions(requestDocument);
	if (fileUploads.length > 0) {
		return { markdown, files: fileUploads };
	}
	return markdown;
}

function messageCarryingItsOptions(requestDocument: ReplySendRequest): string {
	const options = requestDocument.interaction?.options ?? [];
	if (options.length === 0) {
		return requestDocument.message;
	}
	const written = requestDocument.message.trim();
	const question = (requestDocument.interaction?.question ?? "").trim();
	const stillToAsk = written.includes(question) ? "" : question;
	const numbered = options
		.map((option, position) => `${position + 1}. ${option.label}`)
		.join("\n");
	return [written, stillToAsk, numbered].filter((part) => part !== "").join("\n\n");
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
			avatarURL: conversation.avatarURL,
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

// A caller that was never spoken to in a channel holds no cursor for it, so the
// channel's own name is enough to start reading, the way message.post already
// resolves one to post into.
async function historyThreadIdForChannel(
	adapter: PlatformChatAdapter,
	requestDocument: { channelID?: string; channelName?: string },
): Promise<string> {
	const channelID = await resolveMessagePostChannelID(adapter, {
		channelID: requestDocument.channelID,
		channelName: requestDocument.channelName,
		message: "",
	});
	return adapter.encodeThreadId({ channelId: channelID });
}

const messageSearchDefaultLimit = 25;
const messageSearchMostResults = 50;

async function handleMessageSearch(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<MessageSearchResponse> {
	const requestDocument = parseMessageSearchRequest(requestBody);
	if (!(adapter instanceof BuzzAdapter)) {
		throw new MalformedRequest(`platform ${adapter.name} does not serve message.search`);
	}
	const channelId = await resolveMessageSearchChannelId(adapter, requestDocument);
	const candidates = await adapter.searchMessages({
		channelId,
		rootEventId: requestDocument.rootMessageID?.trim() || undefined,
		messageIds: requestDocument.messageIDs?.length ? requestDocument.messageIDs : undefined,
		authorPubkeyHex: messageSearchAuthorPubkey(adapter, requestDocument),
		queries: requestDocument.queries ?? [],
		limit: messageSearchLimit(requestDocument.limit),
	});
	return { channelID: channelId, candidates };
}

function messageSearchLimit(limit: number | undefined): number {
	if (!limit || limit <= 0) return messageSearchDefaultLimit;
	return Math.min(limit, messageSearchMostResults);
}

function messageSearchAuthorPubkey(
	adapter: BuzzAdapter,
	requestDocument: MessageSearchRequest,
): string | undefined {
	switch (requestDocument.authoredBy?.trim() ?? "") {
		case "assistant":
			return adapter.botPubkey;
		case "requester": {
			const pubkey = requestDocument.requesterPubkeyHex?.trim();
			if (!pubkey) {
				throw new MalformedRequest("authoredBy=requester needs requesterPubkeyHex");
			}
			return pubkey;
		}
		default:
			return undefined;
	}
}

async function resolveMessageSearchChannelId(
	adapter: BuzzAdapter,
	requestDocument: MessageSearchRequest,
): Promise<string> {
	if (requestDocument.channelID?.trim()) {
		return requestDocument.channelID.trim();
	}
	if (requestDocument.channelName?.trim()) {
		const channelId = await adapter.channelIdByName(requestDocument.channelName);
		if (!channelId) {
			throw new MalformedRequest(
				`no channel named ${JSON.stringify(requestDocument.channelName)} exists on ${adapter.name}`,
			);
		}
		return channelId;
	}
	const replyTargetID = requestDocument.replyTargetID?.trim();
	if (!replyTargetID) {
		throw new MalformedRequest("message.search requires replyTargetID, channelID, or channelName");
	}
	return adapter.decodeThreadId(replyTargetID).channelId;
}

async function handleHistoryFetch(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<HistoryFetchResponse> {
	const requestDocument = parseHistoryFetchRequest(requestBody);
	const { threadId, cursor } = requestDocument.historyCursor
		? decodeHistoryCursor(requestDocument.historyCursor)
		: {
				threadId: requestDocument.threadID ?? (await historyThreadIdForChannel(adapter, requestDocument)),
				cursor: undefined,
			};
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

async function handleAttachmentsImport(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<AttachmentImportResponse> {
	const requestDocument = parseAttachmentImportRequest(requestBody);
	const platformName = adapter.name;
	if (!supportsAttachmentFetching(adapter)) {
		return {
			inputParts: [],
			inputAttachments: requestDocument.inputAttachments.map((attachment) => ({
				...attachment,
				isAvailable: false,
				errorCode: "unsupported_platform",
				message: `platform ${platformName} cannot fetch attachments`,
			})),
		};
	}
	const importedAttachments = await Promise.all(
		requestDocument.inputAttachments.map((attachment) =>
			fetchAttachmentForDirectory(adapter, requestDocument.targetDirectoryPath, attachment),
		),
	);

	return {
		inputAttachments: importedAttachments,
		inputParts: importedAttachments
			.filter((attachment) => attachment.isAvailable && attachment.path)
			.map((attachment) => agentPartForAttachment(adapter.name, attachment, requestDocument.messageID)),
	};
}

function agentPartForAttachment(
	platform: string,
	attachment: InputAttachmentDocument,
	messageID: string,
): AgentPartDocument {
	const isImage = (attachment.contentType ?? "").startsWith("image/");
	const source = { platform: attachment.platform || platform, messageID, fileID: attachment.fileID };
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

type ChannelLookupAdapter = {
	channelIdByName(name: string): Promise<string | undefined>;
};

function supportsChannelLookup(adapter: object): adapter is ChannelLookupAdapter {
	return typeof (adapter as ChannelLookupAdapter).channelIdByName === "function";
}

async function handleMessagePost(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<MessagePostResponse> {
	const requestDocument = parseMessagePostRequest(requestBody);
	const fileUploads = await buildFileUploads(requestDocument.attachments ?? []);
	const message: AdapterPostableMessage =
		fileUploads.length > 0 ? { markdown: requestDocument.message, files: fileUploads } : requestDocument.message;
	if (requestDocument.threadID) {
		const posted = await adapter.postMessage(requestDocument.threadID, message);
		return { messageID: posted.id };
	}
	const channelID = await resolveMessagePostChannelID(adapter, requestDocument);
	const posted = await adapter.postChannelMessage(channelID, message);
	return { messageID: posted.id, channelID };
}

async function resolveMessagePostChannelID(
	adapter: PlatformChatAdapter,
	requestDocument: MessagePostRequest,
): Promise<string> {
	// A channelID arrives from a model that may have reused whatever id it last
	// saw. When the caller also names the channel, the name is resolved and has
	// to agree; a mismatch fails closed instead of posting into the wrong room.
	if (requestDocument.channelID && requestDocument.channelName && supportsChannelLookup(adapter)) {
		const resolvedID = await adapter.channelIdByName(requestDocument.channelName);
		if (resolvedID && resolvedID !== requestDocument.channelID) {
			throw new MalformedRequest(
				`channelID ${requestDocument.channelID} is not the channel named ${JSON.stringify(requestDocument.channelName)} (${resolvedID}); pass one or the other`,
			);
		}
		return resolvedID ?? requestDocument.channelID;
	}
	if (requestDocument.channelID) {
		return requestDocument.channelID;
	}
	const channelName = requestDocument.channelName ?? "";
	if (!supportsChannelLookup(adapter)) {
		throw new MalformedRequest(`platform ${adapter.name} cannot resolve a channel by name; pass channelID`);
	}
	const channelID = await adapter.channelIdByName(channelName);
	if (!channelID) {
		throw new MalformedRequest(`no channel named ${JSON.stringify(channelName)} exists on ${adapter.name}`);
	}
	return channelID;
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

// The agent's own platform identity lives with the adapter that signs as it,
// so a caller that must name the bot asks here instead of deriving the key.
async function handleIdentitySelf(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	_requestBody: unknown,
): Promise<IdentitySelfResponse> {
	if (!(adapter instanceof BuzzAdapter)) {
		throw new MalformedRequest(`platform ${adapter.name} does not serve identity.self`);
	}
	return { pubkeyHex: adapter.botPubkey, name: adapter.userName };
}

// Only buzz carries a marked reply tag; the other adapters thread by the target
// they were already given and take nothing here.
function answeringTagsFor(adapter: unknown, answeringMessageID: string | undefined): string[][] {
	const answered = answeringMessageID?.trim();
	if (!answered || !(adapter instanceof BuzzAdapter)) return [];
	return [["e", answered, "", "reply"]];
}
