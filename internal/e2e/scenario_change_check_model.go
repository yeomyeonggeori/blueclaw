package e2e

import (
	"context"
	"fmt"
	"sync"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type scenarioChangeChecks struct {
	mutex     sync.Mutex
	turnIndex int
	answers   []map[string]float64
}

func scenarioChangeCheckModel(scriptedModel *agenttest.ScriptedLanguageModel, changeChecks *scenarioChangeChecks) model.DecisionModel {
	if scriptedModel == nil {
		return nil
	}
	return changeChecks
}

func (changeChecks *scenarioChangeChecks) beginTurn(turnIndex int, answers []map[string]float64) {
	changeChecks.mutex.Lock()
	defer changeChecks.mutex.Unlock()
	changeChecks.turnIndex = turnIndex
	changeChecks.answers = append([]map[string]float64{}, answers...)
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
	for questionName := range request.Questions {
		noul, isScripted := scripted[questionName]
		if !isScripted {
			return model.DecisionResponse{}, fmt.Errorf("turn %d scripted no answer for change check question %s", changeChecks.turnIndex, questionName)
		}
		answers[questionName] = model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: noul}
	}
	return model.DecisionResponse{Answers: answers, ModelName: scenarioDecisionModelName}, nil
}
