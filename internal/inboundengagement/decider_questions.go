package inboundengagement

import (
	"slices"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type reactionEmoji struct {
	name        string
	description string
}

var reactionEmojis = []reactionEmoji{
	{"white_check_mark", "acknowledged, seen"},
	{"eyes", "looking at it now"},
	{"+1", "agreement or approval"},
	{"saluting_face", "on it, will do"},
	{"pray", "thanks, or please, aimed at the assistant"},
	{"heart", "warmth or appreciation"},
	{"tada", "celebration of a result"},
	{"clap", "praise for someone's work"},
	{"raised_hands", "shared celebration or gratitude"},
	{"fire", "impressive results"},
	{"rocket", "a launch or shipped work"},
	{"sparkles", "something new or polished"},
	{"100", "strong agreement with an impressive result"},
	{"muscle", "cheering effort on"},
	{"wave", "a greeting or a farewell"},
	{"thinking_face", "an open question worth considering"},
	{"memo", "noted"},
	{"bulb", "a good idea"},
	{"sob", "sympathy for bad news"},
	{"joy", "laughing at a joke"},
	{"slightly_smiling_face", "a friendly greeting or a kind word, lightly returned"},
	{"sweat_smile", "an awkward or self-deprecating joke"},
}

var gatewayQuestionNames = []string{
	QuestionTarget,
	QuestionShouldRespond,
	QuestionWork,
	QuestionReaction,
	QuestionReactionEmoji,
	QuestionDuty,
	QuestionRelatesToActiveTask,
	QuestionBusyRoute,
}

func AsksOnlyGatewayQuestions(questions map[string]model.DecisionQuestion) bool {
	for questionName := range questions {
		if !slices.Contains(gatewayQuestionNames, questionNameWithoutMessageKey(questionName)) {
			return false
		}
	}
	return len(questions) > 0
}

func questionNameWithoutMessageKey(questionName string) string {
	_, name, _ := strings.Cut(questionName, ".")
	return name
}

func messageKey(index int) string {
	return "m" + strconv.Itoa(index+1)
}

func isChannel(facts Facts) bool {
	return IsMultiPersonConversation(facts.ConversationType)
}

func asksTarget(facts Facts) bool {
	return isChannel(facts)
}

func asksDuty(facts Facts, message Message) bool {
	return isChannel(facts) && !message.BotMentioned && len(facts.Duties) > 0
}

func asksAnyDuty(facts Facts) bool {
	for _, message := range facts.Messages {
		if asksDuty(facts, message) {
			return true
		}
	}
	return false
}

func asksBusyRoute(facts Facts) bool {
	return facts.OpenTask != nil
}

func asksRelatesToActiveTask(facts Facts) bool {
	return facts.OpenTask != nil || facts.FinishedTask != nil
}

func newDecisionRequest(facts Facts) model.DecisionRequest {
	questions := map[string]model.DecisionQuestion{}
	for index, message := range facts.Messages {
		key := messageKey(index)
		for name, question := range questionsForMessage(facts, message, key) {
			questions[key+"."+name] = question
		}
	}
	return model.DecisionRequest{State: newDecisionState(facts), Questions: questions}
}

func questionsForMessage(facts Facts, message Message, key string) map[string]model.DecisionQuestion {
	agentName := facts.AgentIdentity.DisplayName()
	about := "About message " + key + " in the state. "
	questions := addressingQuestions(about, agentName)
	if asksTarget(facts) {
		questions[QuestionTarget] = targetQuestion(about, agentName)
	}
	if asksDuty(facts, message) {
		questions[QuestionDuty] = dutyQuestion(about, agentName, facts.Duties)
	}
	if asksRelatesToActiveTask(facts) {
		questions[QuestionRelatesToActiveTask] = relatesToActiveTaskQuestion(about)
	}
	if asksBusyRoute(facts) {
		questions[QuestionBusyRoute] = busyRouteQuestion(about)
	}
	return questions
}

func targetQuestion(about string, agentName string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about + "Who is it directed at? " + agentName + " is the workplace assistant in this conversation.",
		OptionDescriptions: map[string]string{
			string(AddressingTargetBot):     "directed at " + agentName + ", by mention, by reply, or by an unmistakable request to it",
			string(AddressingTargetHuman):   "directed at one specific person other than " + agentName,
			string(AddressingTargetAnyone):  "directed at the room in general, a share or an announcement anyone may answer",
			string(AddressingTargetNone):    "directed at nobody, a self-note, a reaction, or filler",
			string(AddressingTargetUnclear): "genuinely impossible to tell who it is aimed at",
		},
	}.Question()
}

func workQuestion(about string, agentName string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about + "Does it ask " + agentName + " to do work that takes tools and time, and how much? Work asked of somebody else is none.",
		OptionDescriptions: map[string]string{
			WorkOptionNone:       "nothing for " + agentName + " to do. Words alone answer it, from what is visible, common knowledge or judgment, including a translation, an explanation or a draft written in the reply; or nobody asked " + agentName + " for anything",
			WorkOptionEasy:       "a short piece of work: a lookup, one record or one change",
			WorkOptionNormal:     "work in several steps: research, several records, or a document or file to produce",
			WorkOptionHard:       "long, wide or verification-heavy work",
			WorkOptionImpossible: "work that cannot be done: physically impossible, nonsensical, or plainly improper on its face. Never for a permission concern, which the operating system decides when the work runs",
		},
	}.Question()
}

func addressingQuestions(about string, agentName string) map[string]model.DecisionQuestion {
	return map[string]model.DecisionQuestion{
		QuestionWork: workQuestion(about, agentName),
		QuestionShouldRespond: model.NoulQuestion{
			Instructions:    about + "Should " + agentName + " write a text reply to it?",
			TrueDescription: "it is a direct request, question, or instruction to " + agentName + "; it answers a question " + agentName + " asked; it makes " + agentName + " the intended responder; or it is social or playful and aimed at " + agentName + ", where a short in-kind reply keeps the conversation going",
			FalseDescription: "anything else. Ignore is the normal outcome for channel traffic: work chatter between other people, their status updates and coordination, thanks between two other people, " +
				"and a share, an FYI, or a closing thanks aimed at " + agentName + " that wants no words back",
		}.Question(),
		QuestionReaction: model.ChoiceQuestion{
			Instructions: about + "Would a courteous coworker leave an emoji reaction on it?",
			OptionDescriptions: map[string]string{
				ReactionOptionNone: "no reaction; reacting would be noise. Routine work chatter between other people, status exchanges between colleagues, personal thanks between two people, and any message that neither addresses nor includes " + agentName + " get nothing. Topic or wording alone is never a reason to react",
				ReactionOptionReact: "a single emoji acknowledges it well: a share or FYI posted for the whole team or for " + agentName + ", news worth celebrating, a joke posted for the room, " +
					"or a closing thanks or acknowledgement aimed at " + agentName,
			},
		}.Question(),
		QuestionReactionEmoji: model.ChoiceQuestion{
			Instructions:       about + "If a reaction were added to it, which emoji fits best?",
			OptionDescriptions: reactionEmojiOptionDescriptions(),
		}.Question(),
	}
}

func reactionEmojiOptionDescriptions() map[string]string {
	descriptions := map[string]string{}
	for _, emoji := range reactionEmojis {
		descriptions[emoji.name] = emoji.description
	}
	return descriptions
}

func dutyQuestion(about string, agentName string, duties []StandingDuty) model.DecisionQuestion {
	descriptions := map[string]string{DutyOptionNone: "it records nothing: chatter, a question, a share, or a request aimed at the assistant itself"}
	for _, duty := range duties {
		descriptions[duty.Name] = duty.Description
	}
	return model.ChoiceQuestion{
		Instructions:       about + "Does it specify a concrete item a standing duty should record right now, even though it was not addressed to " + agentName + "? The duties are listed in standingDuties in the state. Answer none for vague mentions, opinions, questions, hypotheticals, and chit-chat, and for anything addressed to " + agentName + " as a request.",
		OptionDescriptions: descriptions,
	}.Question()
}

func relatesToActiveTaskQuestion(about string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     about + "Does it continue, correct, cancel, or ask about the task in the state (activeTask or recentlyFinishedTask)?",
		TrueDescription:  "it is about that task: a correction, an addition, a cancellation, or a question about its progress",
		FalseDescription: "it is a self-contained new request that has nothing to do with that task",
	}.Question()
}

func busyRouteQuestion(about string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about + "A task is open (activeTask in the state). Its status says whether it is running or waiting: waiting_approval for a yes or no, waiting_user_input for an answer, and postedQuestion is what it asked. What should happen to it? Natural-language stop requests are ordinary messages, so read them by intent.",
		OptionDescriptions: map[string]string{
			string(BusyRouteStatus):    "it asks whether work is happening, asks for progress, or asks about the task or what it is waiting for",
			string(BusyRouteSteer):     "it corrects or redirects the task without cancelling it, including a change to what a waiting task asked about",
			string(BusyRouteReplace):   "it clearly cancels or replaces the task with a new instruction",
			string(BusyRouteCancel):    "it asks to stop, cancel, abort, or not continue the task",
			string(BusyRouteNewTask):   "it is independent and should not affect the task",
			string(BusyRouteUnrelated): "it should neither start nor alter work",
		},
	}.Question()
}
