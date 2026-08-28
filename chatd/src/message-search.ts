// Finding "the post where I said ~~" is a ranking problem, not a lookup: the
// person remembers meaning, not wording. A query that appears verbatim wins
// outright; otherwise tokens shared with the message carry a partial score, so
// near-misses surface as ordered candidates for the model to choose between.
// The score only orders candidates — it never settles which message was meant.

const substringScore = 1;
const tokenMatchCeiling = 0.9;
const shortestPartialTokenLength = 2;

export type ScoredCandidate<Candidate> = { candidate: Candidate; score: number };

export function searchScore(text: string, queries: string[]): number {
	if (queries.length === 0) return substringScore;
	const normalizedText = normalize(text);
	const textTokens = tokensOf(normalizedText);
	let best = 0;
	for (const query of queries) {
		const normalizedQuery = normalize(query);
		if (normalizedQuery === "") continue;
		if (normalizedText.includes(normalizedQuery)) return substringScore;
		best = Math.max(best, tokenOverlapScore(textTokens, tokensOf(normalizedQuery)));
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
