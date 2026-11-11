import type { AnsweredChoice, PersonalAsk, PersonalAskOption, PersonalAskResolution } from "../../choice.ts";

type AskKind = PersonalAsk["kind"];

export type AskedQuestion = {
	interactionID: string;
	kind: AskKind;
	targetExternalID: string;
	options: { key: string; label: string }[];
	recommendedOptionKey?: string;
};

const ASK_TAG = "ask";
const OPTION_TAG = "option";
const RECOMMENDED_MARK = "recommended";
const RESOLVED_TAG = "resolved";
const ANSWER_TAG = "answer";

export function askKindOf(interactionKind: string | undefined): AskKind {
	return interactionKind === "approval" ? "approval" : "choice";
}

export function askTags(question: AskedQuestion): string[][] {
	const optionTags = question.options.map((option) =>
		option.key === question.recommendedOptionKey
			? [OPTION_TAG, option.key, option.label, RECOMMENDED_MARK]
			: [OPTION_TAG, option.key, option.label],
	);
	return [[ASK_TAG, question.interactionID, question.kind, question.targetExternalID], ...optionTags];
}

export function askOf(tags: string[][]): PersonalAsk | undefined {
	const askTag = tags.find((tag) => tag[0] === ASK_TAG);
	const interactionID = askTag?.[1];
	if (!askTag || !interactionID) return undefined;
	const options = tags.filter((tag) => tag[0] === OPTION_TAG).flatMap(optionOf);
	if (options.length === 0) return undefined;
	return {
		interactionID,
		kind: askKindOf(askTag[2]),
		targetExternalID: askTag[3] ?? "",
		options,
	};
}

function optionOf(tag: string[]): PersonalAskOption[] {
	const [, key, label, mark] = tag;
	if (!key) return [];
	return [{ key, label: label ?? "", isRecommended: mark === RECOMMENDED_MARK }];
}

export function resolvedTag(interactionID: string, choiceKey: string | undefined): string[] {
	return choiceKey ? [RESOLVED_TAG, interactionID, choiceKey] : [RESOLVED_TAG, interactionID];
}

export function recordsAResolution(tags: string[][]): boolean {
	return tags.some((tag) => tag[0] === RESOLVED_TAG);
}

export function resolutionOf(tags: string[][], interactionID: string): PersonalAskResolution | undefined {
	const tag = tags.find((candidate) => candidate[0] === RESOLVED_TAG && candidate[1] === interactionID);
	if (!tag) return undefined;
	const key = tag[2];
	return key ? { key } : {};
}

export function answerTags(answer: AnsweredChoice | undefined): string[][] {
	return answer ? [[ANSWER_TAG, answer.interactionID, answer.key]] : [];
}

export function answeredChoiceOf(tags: string[][]): AnsweredChoice | undefined {
	const tag = tags.find((candidate) => candidate[0] === ANSWER_TAG);
	const interactionID = tag?.[1];
	const key = tag?.[2];
	if (!interactionID || !key) return undefined;
	return { interactionID, key };
}
