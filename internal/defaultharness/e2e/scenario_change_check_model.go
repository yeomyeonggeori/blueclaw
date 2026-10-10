//go:build !nobundledharness

package e2e

import (
	"context"
	"fmt"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type scenarioChangeChecks struct {
	mutex     sync.Mutex
	turnIndex int
	answers   []map[string]any
}

func scenarioChangeCheckModel(scriptedModel *agenttest.ScriptedLanguageModel, changeChecks *scenarioChangeChecks) model.DecisionModel {
	if scriptedModel == nil {
		return nil
	}
	return changeChecks
}

func (changeChecks *scenarioChangeChecks) beginTurn(turnIndex int, answers []map[string]any) {
	changeChecks.mutex.Lock()
	defer changeChecks.mutex.Unlock()
	changeChecks.turnIndex = turnIndex
	changeChecks.answers = append([]map[string]any{}, answers...)
}

func (changeChecks *scenarioChangeChecks) pendingCount() int {
	changeChecks.mutex.Lock()
	defer changeChecks.mutex.Unlock()
	return len(changeChecks.answers)
}

func (changeChecks *scenarioChangeChecks) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	changeChecks.mutex.Lock()
	defer changeChecks.mutex.Unlock()
	if len(changeChecks.answers) == 0 {
		return model.DecisionResponse{}, fmt.Errorf("turn %d asked for a change check its script does not hold", changeChecks.turnIndex)
	}
	scripted := changeChecks.answers[0]
	changeChecks.answers = changeChecks.answers[1:]
	answers := map[string]model.DecisionAnswer{}
	for questionName, question := range request.Questions {
		answer, errorValue := scriptedChangeCheckAnswer(question, scripted[questionName])
		if errorValue != nil {
			return model.DecisionResponse{}, fmt.Errorf("turn %d change check question %s: %w", changeChecks.turnIndex, questionName, errorValue)
		}
		answers[questionName] = answer
	}
	return model.DecisionResponse{Answers: answers, ModelName: scenarioDecisionModelName}, nil
}

func scriptedChangeCheckAnswer(question model.DecisionQuestion, scripted any) (model.DecisionAnswer, error) {
	switch value := scripted.(type) {
	case float64:
		if question.Type != model.DecisionQuestionTypeNoul {
			return model.DecisionAnswer{}, fmt.Errorf("scripted a probability for a %s question", question.Type)
		}
		return model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: value}, nil
	case string:
		if question.Type != model.DecisionQuestionTypeChoice {
			return model.DecisionAnswer{}, fmt.Errorf("scripted the choice %q for a %s question", value, question.Type)
		}
		if !offersOption(question.Criteria, value) {
			return model.DecisionAnswer{}, fmt.Errorf("scripted the choice %q, which the question does not offer", value)
		}
		return model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: value, Probabilities: map[string]float64{value: 1}}, nil
	default:
		return model.DecisionAnswer{}, fmt.Errorf("scripted no answer")
	}
}

func offersOption(criteria any, option string) bool {
	switch options := criteria.(type) {
	case map[string]string:
		_, isOffered := options[option]
		return isOffered
	case map[string]any:
		_, isOffered := options[option]
		return isOffered
	default:
		return false
	}
}
