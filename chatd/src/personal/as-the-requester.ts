import { heldSigningSecret, secretHexPattern } from "../message-ownership.ts";
import { CredentialRefused, UnsupportedByPlatform, type ActorCredential, type PersonalGateway } from "./gateway.ts";
import type { PersonCapability } from "./capabilities.ts";
import { MalformedRequest } from "./parse.ts";

const buzzSecretKind = "buzz-secret";

export const personCapabilityAskedAsTheRequester: Record<string, string> = {
	"requester.conversations.list": "person.conversations.list",
	"requester.channels.open.list": "person.channels.open.list",
	"requester.channel.create": "person.channel.create",
	"requester.channel.join": "person.channel.join",
	"requester.channel.leave": "person.channel.leave",
	"requester.channel.members.add": "person.channel.members.add",
	"requester.channel.member.remove": "person.channel.member.remove",
	"requester.channel.owner.set": "person.channel.owner.set",
	"requester.channel.owner.add": "person.channel.owner.add",
	"requester.channel.delete": "person.channel.delete",
};

export function askedAsTheRequester(capability: PersonCapability, admindBaseURL: string | undefined): PersonCapability {
	return async (gateway, body, agentExternalID) => {
		const actor = await requesterActor(gateway, body, admindBaseURL);
		return await capability(gateway, { ...requestRecord(body), actor }, agentExternalID);
	};
}

async function requesterActor(
	gateway: PersonalGateway,
	body: unknown,
	admindBaseURL: string | undefined,
): Promise<ActorCredential> {
	if (gateway.credentialKind !== buzzSecretKind) {
		throw new UnsupportedByPlatform(gateway.platform, "act for the person who asked the agent");
	}
	const pubkeyHex = requesterPubkeyOf(body);
	if (!admindBaseURL) {
		throw new CredentialRefused(`this device cannot sign as ${pubkeyHex}: no service holding signing keys is configured`);
	}
	const secret = await heldSigningSecret(
		pubkeyHex,
		admindBaseURL,
		(reason) => new CredentialRefused(`this device cannot act as ${pubkeyHex}: ${reason}`),
	);
	return { kind: gateway.credentialKind, secret };
}

function requesterPubkeyOf(body: unknown): string {
	const given = requestRecord(body).requesterPubkeyHex;
	const pubkeyHex = typeof given === "string" ? given.trim().toLowerCase() : "";
	if (!secretHexPattern.test(pubkeyHex)) {
		throw new MalformedRequest("requesterPubkeyHex must name the person who asked, as 64 hexadecimal characters");
	}
	return pubkeyHex;
}

function requestRecord(body: unknown): Record<string, unknown> {
	if (typeof body !== "object" || body === null || Array.isArray(body)) {
		throw new MalformedRequest("expected a JSON object");
	}
	return body as Record<string, unknown>;
}
