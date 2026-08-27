import path from "node:path";
import type { InputAttachmentDocument } from "./outbound-types.ts";

export type AttachmentFetchingAdapter = {
	fetchAttachment(attachment: InputAttachmentDocument): Promise<Response>;
};

export function supportsAttachmentFetching(adapter: object): adapter is AttachmentFetchingAdapter {
	return typeof (adapter as AttachmentFetchingAdapter).fetchAttachment === "function";
}

// This process runs beside the workspace, not inside it, so it fetches the file
// and hands over the bytes with the path they belong at. Whoever owns that
// workspace writes them there, as the person the message was sent to.
export async function fetchAttachmentForDirectory(
	adapter: AttachmentFetchingAdapter,
	targetDirectoryPath: string,
	attachment: InputAttachmentDocument,
): Promise<InputAttachmentDocument> {
	if (!attachment.fileID && !attachment.url) {
		return { ...attachment, isAvailable: false, errorCode: "missing_file_reference" };
	}
	let downloadResponse: Response;
	try {
		downloadResponse = await adapter.fetchAttachment(attachment);
	} catch (reason) {
		return {
			...attachment,
			isAvailable: false,
			errorCode: "download_failed",
			message: reason instanceof Error ? reason.message : String(reason),
		};
	}
	if (!downloadResponse.ok) {
		return {
			...attachment,
			isAvailable: false,
			errorCode: "download_failed",
			message: `attachment download returned ${downloadResponse.status}`,
		};
	}

	const fileBytes = new Uint8Array(await downloadResponse.arrayBuffer());
	const filename = attachmentFilename(attachment);

	return {
		...attachment,
		filename,
		path: path.join(targetDirectoryPath, filename),
		contentBase64: Buffer.from(fileBytes).toString("base64"),
		isAvailable: true,
		sizeBytes: fileBytes.byteLength,
		contentType: attachment.contentType ?? downloadResponse.headers.get("content-type") ?? undefined,
	};
}

function attachmentFilename(attachment: InputAttachmentDocument): string {
	const named = attachment.filename?.trim();
	if (named) return path.basename(named);
	if (attachment.fileID) return attachment.fileID;
	if (attachment.url) {
		const lastSegment = attachment.url.split("?")[0]?.split("/").pop();
		if (lastSegment) return lastSegment;
	}
	return "attachment";
}
