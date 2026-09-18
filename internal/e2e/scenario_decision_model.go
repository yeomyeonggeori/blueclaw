package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const scenarioIgnoringAddressingResponse = `{"target":"anyone","shouldRespond":false,"dutyMatch":false,"dutyName":"","dutyConfidence":0}`

const scenarioDecisionModelName = "scenario-decision-model"

const scenarioAddressingOnlyTurn = "addressing_only"

type scenarioTurnScript struct {
	turnIndex     int
	turnDocuments []string
}

func (turnScript *scenarioTurnScript) beginTurn(turnIndex int, turnDocuments []string) {
	turnScript.turnIndex = turnIndex
	turnScript.turnDocuments = append([]string{}, turnDocuments...)
}

func (turnScript *scenarioTurnScript) next() (string, error) {
	if len(turnScript.turnDocuments) == 0 {
		return "", fmt.Errorf("turn %d asked for a decision its script does not hold", turnScript.turnIndex)
	}
	turnDocument := turnScript.turnDocuments[0]
	turnScript.turnDocuments = turnScript.turnDocuments[1:]
	return turnDocument, nil
}

func (turnScript *scenarioTurnScript) pendingCount() int {
	return len(turnScript.turnDocuments)
}

type scenarioDecisionModel struct {
	turnScript        *scenarioTurnScript
	languageModelTurn *intaketest.LanguageModelDecisionModel
	addressing        agentcontract.AddressingDecision

	mutex          sync.Mutex
	decidedOutcome intaketest.Outcome
	hasDecidedTurn bool
}

func newScenarioDecisionModel(turnScript *scenarioTurnScript, languageModel model.LanguageModelProvider, addressingResponse string) *scenarioDecisionModel {
	addressing := scenarioAddressingDecision(addressingResponse)
	return &scenarioDecisionModel{
		turnScript: turnScript,
		languageModelTurn: &intaketest.LanguageModelDecisionModel{
			LanguageModel: languageModel,
			Addressing:    addressing,
			ModelName:     scenarioDecisionModelName,
		},
		addressing: addressing,
	}
}

func (decisionModel *scenarioDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionModel.turnScript == nil {
		return decisionModel.languageModelTurn.Decide(ctx, request)
	}
	if outcome, isDecided := decisionModel.decidedTurn(); isDecided && asksOnlyAboutTools(request.Questions) {
		return decisionModel.answersFrom(request, outcome), nil
	}
	turnDocument, errorValue := decisionModel.turnScript.next()
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	if strings.TrimSpace(turnDocument) == scenarioAddressingOnlyTurn {
		return model.DecisionResponse{
			Answers:   intaketest.Answers(request.Questions, decisionModel.addressingOnlyOutcome),
			ModelName: scenarioDecisionModelName,
		}, nil
	}
	var turnDecision agentcontract.TurnDecision
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(turnDocument)), &turnDecision); errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	outcome := intaketest.Outcome{
		Addressing:        decisionModel.addressing,
		TurnDecision:      turnDecision,
		PendingChoiceKeys: intaketest.PendingChoiceKeys(request.State),
	}
	decisionModel.rememberDecidedTurn(outcome)
	return decisionModel.answersFrom(request, outcome), nil
}

func (decisionModel *scenarioDecisionModel) answersFrom(request model.DecisionRequest, outcome intaketest.Outcome) model.DecisionResponse {
	return model.DecisionResponse{
		Answers:   intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return outcome }),
		ModelName: scenarioDecisionModelName,
	}
}

func (decisionModel *scenarioDecisionModel) decidedTurn() (intaketest.Outcome, bool) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	return decisionModel.decidedOutcome, decisionModel.hasDecidedTurn
}

func (decisionModel *scenarioDecisionModel) rememberDecidedTurn(outcome intaketest.Outcome) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	decisionModel.decidedOutcome = outcome
	decisionModel.hasDecidedTurn = true
}

func asksOnlyAboutTools(questions map[string]model.DecisionQuestion) bool {
	for questionName := range questions {
		if !strings.Contains(questionName, "."+agentcontract.IntakeQuestionPrefixTool) {
			return false
		}
	}
	return len(questions) > 0
}

func (decisionModel *scenarioDecisionModel) addressingOnlyOutcome(string) intaketest.Outcome {
	return intaketest.Outcome{Addressing: decisionModel.addressing}
}

func scenarioAddressingDecision(addressingResponse string) agentcontract.AddressingDecision {
	document := strings.TrimSpace(addressingResponse)
	if document == "" {
		document = scenarioIgnoringAddressingResponse
	}
	var decision agentcontract.AddressingDecision
	if errorValue := json.Unmarshal([]byte(document), &decision); errorValue != nil {
		return agentcontract.AddressingDecision{}
	}
	return decision
}
