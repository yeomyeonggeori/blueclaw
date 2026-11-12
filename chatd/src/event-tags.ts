const REPLY_KIND_TAG = "reply-kind";

export type ReplyKind = "progress" | "error";

export function tagsOf(raw: unknown): string[][] {
	if (typeof raw !== "object" || raw === null || !("tags" in raw) || !Array.isArray(raw.tags)) return [];
	return raw.tags.filter(isTag);
}

function isTag(candidate: unknown): candidate is string[] {
	return Array.isArray(candidate) && candidate.every((entry) => typeof entry === "string");
}

export function replyKindTag(kind: ReplyKind): string[] {
	return [REPLY_KIND_TAG, kind];
}

export function carriesReplyKind(tags: string[][], kind: ReplyKind): boolean {
	return tags.some((tag) => tag[0] === REPLY_KIND_TAG && tag[1] === kind);
}
