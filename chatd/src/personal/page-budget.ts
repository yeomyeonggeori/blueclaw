export const defaultPageBudgetBytes = 600_000;

export type BudgetedPage<Entry> = { kept: Entry[]; hasOlder: boolean };

function byteLengthOf(entry: unknown): number {
	return new TextEncoder().encode(JSON.stringify(entry)).length;
}

export function withinBudget<Entry>(
	oldestFirst: Entry[],
	budgetBytes: number = defaultPageBudgetBytes,
): BudgetedPage<Entry> {
	const kept: Entry[] = [];
	let spent = 0;
	for (const entry of [...oldestFirst].reverse()) {
		spent += byteLengthOf(entry);
		if (spent > budgetBytes && kept.length > 0) return { kept, hasOlder: true };
		kept.unshift(entry);
	}
	return { kept, hasOlder: false };
}
