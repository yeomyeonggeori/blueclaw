import type { ActorCredential } from "./gateway.ts";
import type { OutgoingAttachment } from "../outgoing-attachment.ts";

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
	counterpartExternalIDs: string[];
	attachments: OutgoingAttachment[];
};

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
		counterpartExternalIDs: parseCounterparts(record),
		attachments: parseAttachments(record),
	};
}

function parseAttachments(record: Record<string, unknown>): OutgoingAttachment[] {
	const given = record.attachments;
	if (given === undefined) return [];
	if (!Array.isArray(given)) throw new MalformedRequest("attachments must be a list");
	return given.map((entry) => parseAttachment(asRecord(entry)));
}

function parseAttachment(attachment: Record<string, unknown>): OutgoingAttachment {
	const named = {
		filename: requiredText(attachment, "filename"),
		contentType: requiredText(attachment, "contentType"),
	};
	if (attachment.address === undefined) {
		return { ...named, contentBase64: requiredText(attachment, "contentBase64") };
	}
	return {
		...named,
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

export function requireLargestBytes(request: PersonRequest): number {
	if (!request.largestBytes) throw missing("largestBytes");
	return request.largestBytes;
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
