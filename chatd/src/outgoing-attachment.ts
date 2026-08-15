export type OutgoingAttachment = AttachmentToUpload | AttachmentAlreadyKept;

type NamedFile = {
	filename: string;
	contentType: string;
};

export type AttachmentToUpload = NamedFile & {
	contentBase64: string;
};

export type AttachmentAlreadyKept = NamedFile & {
	address: string;
	sizeBytes: number;
	digest: string;
};

export function isAlreadyKept(attachment: OutgoingAttachment): attachment is AttachmentAlreadyKept {
	return "address" in attachment;
}

export type AttachmentRefusal = {
	index: number;
	filename: string;
	status: number;
	reason: string;
};

export class AttachmentRefused extends Error {
	constructor(readonly refusals: AttachmentRefusal[]) {
		super(
			"the messenger's file store refused " +
				refusals.map((refusal) => `${refusal.filename} (${refusal.status}: ${refusal.reason})`).join("; "),
		);
		this.name = "AttachmentRefused";
	}
}
