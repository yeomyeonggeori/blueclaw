package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	languageModelTurn intaketest.LanguageModelDecisionModel
	addressing        agentcontract.AddressingDecision
}

func newScenarioDecisionModel(turnScript *scenarioTurnScript, languageModel model.LanguageModelProvider, addressingResponse string) scenarioDecisionModel {
	addressing := scenarioAddressingDecision(addressingResponse)
	return scenarioDecisionModel{
		turnScript: turnScript,
		languageModelTurn: intaketest.LanguageModelDecisionModel{
			LanguageModel: languageModel,
			Addressing:    addressing,
			ModelName:     scenarioDecisionModelName,
		},
		addressing: addressing,
	}
}

func (decisionModel scenarioDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionModel.turnScript == nil {
		return decisionModel.languageModelTurn.Decide(ctx, request)
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
	return model.DecisionResponse{
		Answers:   intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return outcome }),
		ModelName: scenarioDecisionModelName,
	}, nil
}

func (decisionModel scenarioDecisionModel) addressingOnlyOutcome(string) intaketest.Outcome {
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
