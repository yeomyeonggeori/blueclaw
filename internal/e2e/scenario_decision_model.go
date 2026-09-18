package e2e

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const scenarioIgnoringAddressingResponse = `{"target":"anyone","shouldRespond":false,"dutyMatch":false,"dutyName":"","dutyConfidence":0}`

// newScenarioDecisionModel answers a scenario's intake decision from the turn
// the scenario already scripts for the router schema, so a scenario written
// against the router keeps deciding the same way now that one call decides
// everything.
func newScenarioDecisionModel(languageModel model.LanguageModelProvider, addressingResponse string) intaketest.LanguageModelDecisionModel {
	return intaketest.LanguageModelDecisionModel{
		LanguageModel: languageModel,
		Addressing:    scenarioAddressingDecision(addressingResponse),
		ModelName:     "scenario-decision-model",
	}
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
