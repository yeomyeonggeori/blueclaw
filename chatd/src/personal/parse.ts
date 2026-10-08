import type { ActorCredential, NewPersonalChannel, PersonalMentions } from "./gateway.ts";
import { canonicalChannelName } from "../channels.ts";
import type { AttachmentAlreadyKept } from "../outgoing-attachment.ts";

export class MalformedRequest extends Error {
	constructor(message: string) {
		super(message);
		this.name = "MalformedRequest";
	}
}

function missing(field: string): MalformedRequest {
	return new MalformedRequest(`missing required field ${field}`);
}

export type PersonRequest = {
	actor: ActorCredential;
	conversationID?: string;
	messageID?: string;
	body?: string;
	parentID?: string;
	emoji?: string;
	before?: string;
	name?: string;
	externalID?: string;
	largestBytes?: number;
	arrivalsURL?: string;
	typingURL?: string;
	withdrawalsURL?: string;
	readAt?: string;
	mediaURL?: string;
	range?: string;
	sourceURL?: string;
	contentType?: string;
	counterpartExternalIDs: string[];
	attachments: AttachmentAlreadyKept[];
	mentions?: PersonalMentions;
};

export function parseNewChannel(value: unknown): NewPersonalChannel {
	const record = asRecord(value);
	const visibility = record.visibility;
	if (visibility !== "open" && visibility !== "private") {
		throw new MalformedRequest("visibility must be open or private");
	}
	const name = requireText(record, "name");
	if (!canonicalChannelName(name)) throw new MalformedRequest("a channel name needs more than a # prefix");
	return {
		name,
		description: optionalText(record, "description"),
		visibility,
		memberExternalIDs: parseMemberExternalIDs(record),
	};
}

export function parseMemberExternalIDs(value: unknown): string[] {
	const members = asRecord(value).memberExternalIDs ?? [];
	if (!Array.isArray(members) || members.some((entry) => typeof entry !== "string")) {
		throw new MalformedRequest("memberExternalIDs must be a list of ids");
	}
	return members as string[];
}

const legacyBuzzSecretField = "userSecretHex";
const legacyCounterpartField = "counterpartPubkeyHex";

export function parsePersonRequest(value: unknown): PersonRequest {
	const record = asRecord(value);
	return {
		actor: parseActor(record),
		conversationID: optionalText(record, "conversationID"),
		messageID: optionalText(record, "messageID"),
		body: optionalText(record, "body"),
		parentID: optionalText(record, "parentID"),
		emoji: optionalText(record, "emoji"),
		before: optionalText(record, "before"),
		name: optionalText(record, "name"),
		externalID: optionalText(record, "externalID"),
		largestBytes: optionalCount(record, "largestBytes"),
		arrivalsURL: optionalText(record, "arrivalsURL"),
		typingURL: optionalText(record, "typingURL"),
		withdrawalsURL: optionalText(record, "withdrawalsURL"),
		readAt: optionalText(record, "readAt"),
		mediaURL: optionalText(record, "mediaURL"),
		range: optionalText(record, "range"),
		sourceURL: optionalText(record, "sourceURL"),
		contentType: optionalText(record, "contentType"),
		counterpartExternalIDs: parseCounterparts(record),
		attachments: parseAttachments(record),
		mentions: parseMentions(record),
	};
}

function parseMentions(record: Record<string, unknown>): PersonalMentions | undefined {
	const given = record.mentions;
	if (given === undefined) return undefined;
	const mentions = asRecord(given);
	const externalIDs = mentions.externalIDs ?? [];
	if (!Array.isArray(externalIDs) || externalIDs.some((entry) => typeof entry !== "string")) {
		throw new MalformedRequest("mentions.externalIDs must be a list of ids");
	}
	return { externalIDs: externalIDs as string[], isEveryone: mentions.isEveryone === true };
}

function parseAttachments(record: Record<string, unknown>): AttachmentAlreadyKept[] {
	const given = record.attachments;
	if (given === undefined) return [];
	if (!Array.isArray(given)) throw new MalformedRequest("attachments must be a list");
	return given.map((entry) => parseKeptAttachment(asRecord(entry)));
}

export function parseKeptAttachment(attachment: Record<string, unknown>): AttachmentAlreadyKept {
	return {
		filename: requiredText(attachment, "filename"),
		contentType: requiredText(attachment, "contentType"),
		address: requiredText(attachment, "address"),
		sizeBytes: requiredCount(attachment, "sizeBytes"),
		digest: requiredText(attachment, "digest"),
	};
}

function requiredCount(record: Record<string, unknown>, field: string): number {
	const value = optionalCount(record, field);
	if (value === undefined) throw missing(field);
	return value;
}

function requiredText(record: Record<string, unknown>, field: string): string {
	const value = record[field];
	if (typeof value !== "string" || value.trim() === "") throw missing(field);
	return value;
}

export function parseCredentialAnswers(value: unknown): Record<string, string> {
	const given = asRecord(value).answers;
	if (given === undefined) throw missing("answers");
	const answers: Record<string, string> = {};
	for (const [field, answer] of Object.entries(asRecord(given))) {
		if (typeof answer === "string") answers[field] = answer;
	}
	return answers;
}

export function parseActor(record: Record<string, unknown>): ActorCredential {
	const given = record.actor;
	if (given !== undefined) {
		const actor = asRecord(given);
		return { kind: requireText(actor, "kind"), secret: requireText(actor, "secret") };
	}
	const legacySecret = optionalText(record, legacyBuzzSecretField);
	if (legacySecret) return { kind: "buzz-secret", secret: legacySecret };
	throw missing("actor");
}

export function requireConversation(request: PersonRequest): string {
	if (!request.conversationID) throw missing("conversationID");
	return request.conversationID;
}

export function requireReadAt(request: PersonRequest): Date {
	if (!request.readAt) throw missing("readAt");
	const readAt = new Date(request.readAt);
	if (Number.isNaN(readAt.getTime())) throw new MalformedRequest("readAt must be an ISO 8601 time");
	return readAt;
}

export function requireMessage(request: PersonRequest): string {
	if (!request.messageID) throw missing("messageID");
	return request.messageID;
}

export function requireName(request: PersonRequest): string {
	if (!request.name) throw missing("name");
	return request.name;
}

export function requireExternalID(request: PersonRequest): string {
	if (!request.externalID) throw missing("externalID");
	return request.externalID;
}

export function requireMediaURL(request: PersonRequest): string {
	if (!request.mediaURL) throw missing("mediaURL");
	return request.mediaURL;
}

const singleRangePattern = /^bytes=\d+-\d+$/;

export function requireMediaRange(request: PersonRequest): string {
	if (!request.range) throw missing("range");
	if (!singleRangePattern.test(request.range)) throw new MalformedRequest("range must be one bytes=START-END range");
	return request.range;
}

export function requireMediaSource(request: PersonRequest): { url: string; contentType: string } {
	if (!request.sourceURL) throw missing("sourceURL");
	return { url: request.sourceURL, contentType: request.contentType ?? "application/octet-stream" };
}

export function requireLargestBytes(request: PersonRequest): number {
	if (!request.largestBytes) throw missing("largestBytes");
	return request.largestBytes;
}

const loopbackHostnames = new Set(["127.0.0.1", "localhost"]);

export function requireLoopbackArrivalsURL(request: PersonRequest): string {
	if (!request.arrivalsURL) throw missing("arrivalsURL");
	return loopbackURL("arrivalsURL", request.arrivalsURL);
}

export function optionalLoopbackTypingURL(request: PersonRequest): string | undefined {
	if (!request.typingURL) return undefined;
	return loopbackURL("typingURL", request.typingURL);
}

export function optionalLoopbackWithdrawalsURL(request: PersonRequest): string | undefined {
	if (!request.withdrawalsURL) return undefined;
	return loopbackURL("withdrawalsURL", request.withdrawalsURL);
}

function loopbackURL(field: string, offered: string): string {
	const parsed = URL.parse(offered);
	if (!parsed || parsed.protocol !== "http:" || !loopbackHostnames.has(parsed.hostname)) {
		throw new MalformedRequest(`${field} must be an http address on this machine's loopback`);
	}
	return parsed.toString();
}

function parseCounterparts(record: Record<string, unknown>): string[] {
	const given = record.counterpartExternalIDs;
	if (Array.isArray(given)) return given.filter((entry): entry is string => typeof entry === "string");
	const legacy = optionalText(record, legacyCounterpartField);
	return legacy ? [legacy] : [];
}

function asRecord(value: unknown): Record<string, unknown> {
	if (typeof value !== "object" || value === null) {
		throw new MalformedRequest("expected a JSON object");
	}
	return value as Record<string, unknown>;
}

function requireText(record: Record<string, unknown>, field: string): string {
	const value = record[field];
	if (typeof value !== "string" || value.trim().length === 0) {
		throw missing(field);
	}
	return value;
}

function optionalText(record: Record<string, unknown>, field: string): string | undefined {
	const value = record[field];
	return typeof value === "string" && value.length > 0 ? value : undefined;
}

function optionalCount(record: Record<string, unknown>, field: string): number | undefined {
	const value = record[field];
	return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : undefined;
}
