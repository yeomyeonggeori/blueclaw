package callrecord

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const summaryCharacterLimit = 2000

type Ledger interface {
	AppendTaskEvent(taskRunID string, name string, body string)
}

type Call struct {
	ledger    Ledger
	taskRunID string
	requested agentcontract.ToolCallRequested
}

func Start(ledger Ledger, taskRunID string, descriptor toolcontract.ToolDescriptor, input json.RawMessage) *Call {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if ledger == nil || trimmedTaskRunID == "" {
		return nil
	}
	call := &Call{ledger: ledger, taskRunID: trimmedTaskRunID, requested: agentcontract.ToolCallRequested{
		ObservationID:   newObservationID(),
		ToolName:        descriptor.Name,
		Input:           input,
		SideEffectClass: toolcontract.ToolDefinitionSideEffectClass(descriptor),
	}}
	call.append(agentcontract.ToolTaskEventRequestedSuffix, call.requested)
	return call
}

func (call *Call) Finish(toolResult toolcontract.ToolResult, invocationError error) {
	if call == nil {
		return
	}
	if invocationError != nil {
		toolResult = toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.Unavailable, call.requested.ToolName, invocationError.Error())
	}
	call.append(agentcontract.ToolTaskEventResultSuffix, agentcontract.ToolCallResult{
		ObservationID: call.requested.ObservationID,
		ToolInput:     call.requested.Input,
		Summary:       summaryOf(toolResult),
		Failure:       toolResult.Failure,
		Effects:       toolResult.Effects,
	})
}

func RecordCarriedOut(ledger Ledger, taskRunID string, toolSet *toolcontract.ToolSet, carriedOutCalls []agentcontract.CarriedOutCall) {
	for _, carriedOutCall := range carriedOutCalls {
		descriptor, _ := toolSet.ToolDefinition(carriedOutCall.ToolName)
		descriptor.Name = strings.TrimSpace(carriedOutCall.ToolName)
		Start(ledger, taskRunID, descriptor, carriedOutCall.ToolInput).Finish(carriedOutCall.Result, nil)
	}
}

func (call *Call) append(suffix string, body any) {
	encoded, _ := json.Marshal(body)
	call.ledger.AppendTaskEvent(call.taskRunID, agentcontract.ToolTaskEventName(call.requested.ToolName, suffix), string(encoded))
}

func summaryOf(toolResult toolcontract.ToolResult) string {
	summary := strings.TrimSpace(toolResult.ContentText())
	if toolResult.Failure != nil {
		summary = strings.TrimSpace(toolResult.Failure.Code + ": " + firstNonEmpty(summary, toolResult.Failure.UserSafeSummary))
	}
	characters := []rune(summary)
	if len(characters) <= summaryCharacterLimit {
		return summary
	}
	return string(characters[:summaryCharacterLimit]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func newObservationID() string {
	randomBytes := make([]byte, 8)
	_, _ = rand.Read(randomBytes)
	return "host-" + hex.EncodeToString(randomBytes)
}
