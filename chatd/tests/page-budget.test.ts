import { describe, expect, test } from "bun:test";
import { withinBudget } from "../src/personal/page-budget.ts";

function postOf(id: string, length: number) {
	return { id, body: "x".repeat(length) };
}

describe("cutting a page by what it weighs", () => {
	test("keeps a page that fits, in the order it arrived", () => {
		const page = [postOf("a", 10), postOf("b", 10), postOf("c", 10)];

		const { kept, hasOlder } = withinBudget(page, 10_000);

		expect(kept.map((post) => post.id)).toEqual(["a", "b", "c"]);
		expect(hasOlder).toBe(false);
	});

	test("drops the oldest, because the cursor reads backwards from the newest", () => {
		const page = [postOf("oldest", 400), postOf("middle", 400), postOf("newest", 400)];

		const { kept, hasOlder } = withinBudget(page, 900);

		expect(kept.map((post) => post.id)).toEqual(["middle", "newest"]);
		expect(hasOlder).toBe(true);
	});

	test("keeps the newest even when it alone is over budget, so a reader is never stuck", () => {
		const page = [postOf("older", 100), postOf("enormous", 5_000)];

		const { kept, hasOlder } = withinBudget(page, 500);

		expect(kept.map((post) => post.id)).toEqual(["enormous"]);
		expect(hasOlder).toBe(true);
	});

	test("counts bytes rather than characters", () => {
		const page = [{ id: "a", body: "가".repeat(300) }];

		expect(withinBudget(page, 400).kept).toHaveLength(1);
		expect(withinBudget(page, 400).hasOlder).toBe(false);
	});

	test("an empty page stays empty and claims nothing older", () => {
		expect(withinBudget([], 100)).toEqual({ kept: [], hasOlder: false });
	});
});
