import type { BuzzEvent } from "../adapters/buzz/types.ts";
import { attachmentsOf, type UserMessageAttachment } from "../adapters/buzz/user-session.ts";

export type AttachmentKind = "image" | "video" | "file";

export type ArrivalPreview = {
	preview: string;
	attachmentKinds: AttachmentKind[];
	pictureDigest: string;
	pictureContentType: string;
};

const markdownLinkPattern = /!?\[[^\]]*\]\((\S+?)\)/g;

export function arrivalPreviewOf(event: BuzzEvent): ArrivalPreview {
	const attachments = attachmentsOf(event);
	const attachedURLs = new Set(attachments.map((attachment) => attachment.url));
	return {
		preview: textWithoutLinksTo(event.content, attachedURLs),
		attachmentKinds: attachments.map((attachment) => attachmentKindOf(attachment.contentType)),
		...firstPictureOf(attachments),
	};
}

function firstPictureOf(attachments: UserMessageAttachment[]): Pick<ArrivalPreview, "pictureDigest" | "pictureContentType"> {
	const picture = attachments.find((attachment) => attachmentKindOf(attachment.contentType) === "image" && attachment.digest !== "");
	if (!picture) return { pictureDigest: "", pictureContentType: "" };
	return { pictureDigest: picture.digest, pictureContentType: picture.contentType };
}

function textWithoutLinksTo(content: string, attachedURLs: Set<string>): string {
	if (attachedURLs.size === 0) return content;
	let remaining = content.replace(markdownLinkPattern, (link: string, url: string) => (attachedURLs.has(url) ? "" : link));
	for (const url of attachedURLs) remaining = remaining.split(url).join("");
	return remaining
		.split("\n")
		.filter((line) => line.trim() !== "")
		.join("\n")
		.trim();
}

function attachmentKindOf(contentType: string): AttachmentKind {
	if (contentType.startsWith("image/")) return "image";
	if (contentType.startsWith("video/")) return "video";
	return "file";
}
