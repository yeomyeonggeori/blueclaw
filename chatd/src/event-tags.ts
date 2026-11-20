const REPLY_KIND_TAG = "reply-kind";
const RAW_FAILURE_TAG = ["failure-text", "raw"];

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

export type RawFailure = { isRawFailure?: true; failureCause?: string };

export function rawFailureTag(cause: string | undefined): string[] {
	return cause ? [...RAW_FAILURE_TAG, cause] : [...RAW_FAILURE_TAG];
}

export function rawFailureOf(tags: string[][]): RawFailure {
	const tag = tags.find((candidate) => candidate[0] === RAW_FAILURE_TAG[0] && candidate[1] === RAW_FAILURE_TAG[1]);
	if (!tag) return {};
	return tag[2] ? { isRawFailure: true, failureCause: tag[2] } : { isRawFailure: true };
}
