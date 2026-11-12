// Finding "the post where I said ~~" is a ranking problem, not a lookup: the
// person remembers meaning, not wording. A query that appears verbatim wins
// outright; otherwise tokens shared with the message carry a partial score, so
// near-misses surface as ordered candidates for the model to choose between.
// The score only orders candidates — it never settles which message was meant.

const substringScore = 1;
const tokenMatchCeiling = 0.9;
const shortestPartialTokenLength = 2;

export const messageSearchScanLimit = 500;
export const messageSearchMostResults = 50;

export type ScoredCandidate<Candidate> = { candidate: Candidate; score: number };

export function searchScore(text: string, queries: string[]): number {
	if (queries.length === 0) return substringScore;
	const normalizedText = normalize(text);
	const textTokens = tokensOf(normalizedText);
	let best = 0;
	for (const query of queries) {
		if (containsVerbatim(normalizedText, query)) return substringScore;
		best = Math.max(best, tokenOverlapScore(textTokens, tokensOf(normalize(query))));
	}
	return best;
}

export function rankBySearchScore<Candidate>(
	candidates: Candidate[],
	queries: string[],
	textOf: (candidate: Candidate) => string,
	createdAtOf: (candidate: Candidate) => number,
	limit: number,
): ScoredCandidate<Candidate>[] {
	const scored: ScoredCandidate<Candidate>[] = [];
	for (const candidate of candidates) {
		const score = searchScore(textOf(candidate), queries);
		if (score <= 0) continue;
		scored.push({ candidate, score });
	}
	scored.sort(
		(first, second) =>
			second.score - first.score || createdAtOf(second.candidate) - createdAtOf(first.candidate),
	);
	return scored.slice(0, limit);
}

export function containsVerbatim(text: string, query: string): boolean {
	const normalizedQuery = normalize(query);
	return normalizedQuery !== "" && normalize(text).includes(normalizedQuery);
}

export function newestVerbatimMatches<Candidate>(
	candidates: Candidate[],
	query: string,
	textOf: (candidate: Candidate) => string,
	createdAtOf: (candidate: Candidate) => number,
): Candidate[] {
	return candidates
		.filter((candidate) => containsVerbatim(textOf(candidate), query))
		.sort((first, second) => createdAtOf(second) - createdAtOf(first))
		.slice(0, messageSearchMostResults);
}

function normalize(value: string): string {
	return value.toLowerCase().split(/\s+/).filter(Boolean).join(" ");
}

function tokensOf(normalized: string): string[] {
	return normalized.match(/[\p{L}\p{N}]+/gu) ?? [];
}

function tokenOverlapScore(textTokens: string[], queryTokens: string[]): number {
	if (queryTokens.length === 0) return 0;
	let matched = 0;
	for (const queryToken of queryTokens) {
		if (textTokens.some((textToken) => tokensOverlap(textToken, queryToken))) matched++;
	}
	return (matched / queryTokens.length) * tokenMatchCeiling;
}

// "회식" finds "회식은" and "meeting" finds "meetings": the shorter token may
// sit inside the longer one. Single characters carry too little meaning to
// count as a partial hit.
function tokensOverlap(textToken: string, queryToken: string): boolean {
	if (textToken === queryToken) return true;
	const shorter = textToken.length < queryToken.length ? textToken : queryToken;
	const longer = textToken.length < queryToken.length ? queryToken : textToken;
	return shorter.length >= shortestPartialTokenLength && longer.includes(shorter);
}

// A search reads the conversation one window at a time, newest first. A page
// names the window by the newest second it holds, how many ranked matches of
// it were already answered, and which messages of that second an earlier
// window already read.
export type MessageSearchPage = { until?: number; skip: number; seen: string[] };

export const firstMessageSearchPage: MessageSearchPage = { skip: 0, seen: [] };

type ScannedMessage = { id: string; created_at: number };

export function messageSearchPageAfter(
	page: MessageSearchPage,
	scanned: ScannedMessage[],
	matchedCount: number,
	limit: number,
	scanLimit: number,
): MessageSearchPage | undefined {
	if (scanned.length === 0) return undefined;
	const answeredCount = page.skip + limit;
	if (matchedCount > answeredCount) {
		const newest = Math.max(...scanned.map((message) => message.created_at));
		return { ...page, until: page.until ?? newest, skip: answeredCount };
	}
	if (scanned.length < scanLimit) return undefined;
	const oldest = Math.min(...scanned.map((message) => message.created_at));
	return {
		until: oldest,
		skip: 0,
		seen: scanned.filter((message) => message.created_at === oldest).map((message) => message.id),
	};
}

export function encodeMessageSearchCursor(page: MessageSearchPage): string {
	return Buffer.from(JSON.stringify(page)).toString("base64url");
}

export function decodeMessageSearchCursor(cursor: string): MessageSearchPage | undefined {
	const page = parsedJSON(Buffer.from(cursor, "base64url").toString("utf8"));
	if (typeof page !== "object" || page === null) return undefined;
	const { until, skip, seen } = page as Record<string, unknown>;
	if (until !== undefined && !isCount(until)) return undefined;
	if (!isCount(skip)) return undefined;
	if (!Array.isArray(seen) || !seen.every((id): id is string => typeof id === "string")) return undefined;
	return { until, skip, seen };
}

function isCount(value: unknown): value is number {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function parsedJSON(document: string): unknown {
	try {
		return JSON.parse(document);
	} catch {
		return undefined;
	}
}
