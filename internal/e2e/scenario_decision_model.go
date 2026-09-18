package e2e

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const scenarioIgnoringAddressingResponse = `{"target":"anyone","shouldRespond":false,"dutyMatch":false,"dutyName":"","dutyConfidence":0}`

const scenarioDecisionModelName = "scenario-decision-model"

// scenarioDecisionModel answers a scenario's intake decision from the turn the
// scenario already scripts for the router schema, so a scenario written against
// the router keeps deciding the same way now that one call decides everything.
// A scenario scripts a turn only for the messages it expects the router to be
// asked about, so a message addressed to somebody else has none, and the
// addressing half answers on its own.
type scenarioDecisionModel struct {
	scriptedTurn  intaketest.LanguageModelDecisionModel
	scriptedModel *agenttest.ScriptedLanguageModel
	addressing    agentcontract.AddressingDecision
}

func newScenarioDecisionModel(languageModel model.LanguageModelProvider, scriptedModel *agenttest.ScriptedLanguageModel, addressingResponse string) scenarioDecisionModel {
	addressing := scenarioAddressingDecision(addressingResponse)
	return scenarioDecisionModel{
		scriptedTurn: intaketest.LanguageModelDecisionModel{
			LanguageModel: languageModel,
			Addressing:    addressing,
			ModelName:     scenarioDecisionModelName,
		},
		scriptedModel: scriptedModel,
		addressing:    addressing,
	}
}

func (decisionModel scenarioDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if !decisionModel.hasScriptedTurn() {
		return model.DecisionResponse{
			Answers:   intaketest.Answers(request.Questions, decisionModel.addressingOnlyOutcome),
			ModelName: scenarioDecisionModelName,
		}, nil
	}
	return decisionModel.scriptedTurn.Decide(ctx, request)
}

func (decisionModel scenarioDecisionModel) hasScriptedTurn() bool {
	if decisionModel.scriptedModel == nil {
		return true
	}
	return decisionModel.scriptedModel.PendingResponseCounts()[agentcontract.TurnRouterSchemaName] > 0
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
