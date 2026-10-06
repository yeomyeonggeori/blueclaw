package gatewaytest

import (
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type Outcome struct {
	Addressing          inboundengagement.AddressingDecision
	ReactionProbability float64
	BusyRoute           inboundengagement.BusyRoute
	RelatesToActiveTask bool
}

func Answers(questions map[string]model.DecisionQuestion, outcome Outcome) map[string]model.DecisionAnswer {
	answers := map[string]model.DecisionAnswer{}
	for questionName := range questions {
		_, shortName, _ := strings.Cut(questionName, ".")
		answers[questionName] = answerFor(shortName, outcome)
	}
	return answers
}

func answerFor(shortName string, outcome Outcome) model.DecisionAnswer {
	switch shortName {
	case inboundengagement.QuestionTarget:
		return choiceAnswer(orDefault(string(outcome.Addressing.Target), string(inboundengagement.AddressingTargetBot)))
	case inboundengagement.QuestionShouldRespond:
		return noulAnswer(outcome.Addressing.ShouldRespond)
	case inboundengagement.QuestionReaction:
		return reactionAnswer(outcome)
	case inboundengagement.QuestionReactionEmoji:
		return choiceAnswer(orDefault(outcome.Addressing.ReactionEmoji, inboundengagement.DefaultReactionEmojiName))
	case inboundengagement.QuestionDuty:
		return dutyAnswer(outcome.Addressing)
	case inboundengagement.QuestionRelatesToActiveTask:
		return noulAnswer(outcome.RelatesToActiveTask)
	case inboundengagement.QuestionBusyRoute:
		return choiceAnswer(string(outcome.BusyRoute))
	}
	return model.DecisionAnswer{}
}

func reactionAnswer(outcome Outcome) model.DecisionAnswer {
	probability := outcome.ReactionProbability
	if probability == 0 && strings.TrimSpace(outcome.Addressing.ReactionEmoji) != "" {
		probability = 1
	}
	choice := inboundengagement.ReactionOptionNone
	if probability >= 0.5 {
		choice = inboundengagement.ReactionOptionReact
	}
	return model.DecisionAnswer{
		Type:   model.DecisionQuestionTypeChoice,
		Choice: choice,
		Probabilities: map[string]float64{
			inboundengagement.ReactionOptionNone:  1 - probability,
			inboundengagement.ReactionOptionReact: probability,
		},
		Confidence: 1,
	}
}

func dutyAnswer(addressing inboundengagement.AddressingDecision) model.DecisionAnswer {
	dutyName := inboundengagement.DutyOptionNone
	confidence := float64(0)
	if addressing.DutyMatch {
		dutyName = addressing.DutyName
		confidence = addressing.DutyConfidence
	}
	return model.DecisionAnswer{
		Type:          model.DecisionQuestionTypeChoice,
		Choice:        dutyName,
		Probabilities: map[string]float64{dutyName: 1},
		Confidence:    confidence,
	}
}

func choiceAnswer(choice string) model.DecisionAnswer {
	trimmedChoice := strings.TrimSpace(choice)
	return model.DecisionAnswer{
		Type:          model.DecisionQuestionTypeChoice,
		Choice:        trimmedChoice,
		Probabilities: map[string]float64{trimmedChoice: 1},
		Confidence:    1,
	}
}

func noulAnswer(isYes bool) model.DecisionAnswer {
	if isYes {
		return model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: 1}
	}
	return model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: 0}
}

func orDefault(value string, defaultValue string) string {
	if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
		return trimmedValue
	}
	return defaultValue
}
