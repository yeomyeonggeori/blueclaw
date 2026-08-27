import { mkdir } from "node:fs/promises";
import path from "node:path";
import type { ChatdConfiguration } from "./configuration.ts";
import type { InputAttachmentDocument } from "./outbound-types.ts";

export type AttachmentFetchingAdapter = {
	fetchAttachment(attachment: InputAttachmentDocument): Promise<Response>;
};

export function supportsAttachmentFetching(adapter: object): adapter is AttachmentFetchingAdapter {
	return typeof (adapter as AttachmentFetchingAdapter).fetchAttachment === "function";
}

export async function importAttachmentToDirectory(
	adapter: AttachmentFetchingAdapter,
	configuration: ChatdConfiguration,
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
	const filePath = path.join(targetDirectoryPath, filename);
	const writeDirectoryPath = workspaceWritePath(configuration, targetDirectoryPath);
	await mkdir(writeDirectoryPath, { recursive: true });
	await Bun.write(path.join(writeDirectoryPath, filename), fileBytes);

	return {
		...attachment,
		path: filePath,
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

// The import request names the directory in the workspace vocabulary the agent
// reads it back by (/workspace/...), while chatd writes through the host mount
// of that same tree.
export function workspaceWritePath(configuration: ChatdConfiguration, workspacePath: string): string {
	const rootPath = configuration.workspaceRootPath?.replace(/\/$/, "");
	if (!rootPath) return workspacePath;
	if (workspacePath === "/workspace") return rootPath;
	if (workspacePath.startsWith("/workspace/")) {
		return rootPath + workspacePath.slice("/workspace".length);
	}
	return workspacePath;
}
