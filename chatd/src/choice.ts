export type PersonalAskOption = {
	key: string;
	label: string;
	isRecommended: boolean;
};

export type PersonalAskResolution = {
	key?: string;
};

export type PersonalAsk = {
	interactionID: string;
	kind: "choice" | "approval";
	targetExternalID: string;
	options: PersonalAskOption[];
	resolution?: PersonalAskResolution;
};

export type AnsweredChoice = {
	interactionID: string;
	key: string;
};
