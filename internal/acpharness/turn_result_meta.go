package acpharness

import (
	"encoding/json"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (harness *Harness) UseTurnResultMeta(metaKey string) {
	harness.turnResultMetaKey = metaKey
}

func (harness *Harness) carriedTurnResult(promptResponse acp.PromptResponse) (agentcontract.AgentTurnResult, bool) {
	if harness.turnResultMetaKey == "" {
		return agentcontract.AgentTurnResult{}, false
	}
	value, isPresent := promptResponse.Meta[harness.turnResultMetaKey]
	if !isPresent {
		return agentcontract.AgentTurnResult{}, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return agentcontract.AgentTurnResult{}, false
	}
	turnResult := agentcontract.AgentTurnResult{}
	return turnResult, json.Unmarshal(encoded, &turnResult) == nil
}
