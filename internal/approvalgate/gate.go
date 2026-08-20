package approvalgate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type heldCallRecord struct {
	ToolName          string                   `json:"toolName"`
	ToolInput         json.RawMessage          `json:"toolInput"`
	ApprovedToolInput json.RawMessage          `json:"approvedToolInput,omitempty"`
	ApprovalScope     string                   `json:"approvalScope,omitempty"`
	Confirmation      string                   `json:"confirmation"`
	HarnessSession    mcpserver.HarnessSession `json:"harnessSession"`
}

func (record heldCallRecord) approvedInput() json.RawMessage {
	if len(record.ApprovedToolInput) > 0 {
		return record.ApprovedToolInput
	}
	return record.ToolInput
}

type Gate struct {
	taskRunService         taskstate.TaskRunStore
	languageModel          model.LanguageModelProvider
	approvalTargetResolver ApprovalTargetResolver
}

func (gate *Gate) UseLanguageModel(languageModel model.LanguageModelProvider) {
	gate.languageModel = languageModel
}

func New(taskRunService taskstate.TaskRunStore) *Gate {
	return &Gate{taskRunService: taskRunService}
}

func (gate *Gate) AwaitApproval(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) (mcpserver.ApprovalOutcome, error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	if gate.taskHasApprovedScope(taskRunID, approvalRequest.ApprovalScope) {
		return gate.approvedOutcome(taskRunID, approvalRequest.ToolName), nil
	}
	if decision, isDecided := gate.recordedDecision(taskRunID, approvalRequest); isDecided {
		if decision == mcpserver.ApprovalDecisionApproved {
			return gate.approvedOutcome(taskRunID, approvalRequest.ToolName), nil
		}
		return mcpserver.ApprovalOutcome{Decision: decision}, nil
	}
	resolution := gate.resolveApprovalTarget(ctx, approvalRequest)
	if resolution.namesNothingThatExists() {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnresolvedTarget, Failure: resolution.Failure}, nil
	}
	confirmation := gate.confirmationWording(ctx, approvalRequest, resolution.Target)
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, taskstate.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}, nil
	}
	gate.recordHeldCall(taskRunID, approvalRequest, confirmation, resolution.Target)
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, nil
}

func (gate *Gate) approvedOutcome(taskRunID string, toolName string) mcpserver.ApprovalOutcome {
	gate.taskRunService.AppendTaskEvent(taskRunID, "approval.executed", marshalEventBody(map[string]string{"toolName": toolName}))
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved}
}

func (gate *Gate) taskHasApprovedScope(taskRunID string, approvalScope string) bool {
	requestedScope := strings.TrimSpace(approvalScope)
	if taskRunID == "" || requestedScope == "" {
		return false
	}
	for _, taskEvent := range gate.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != "approval.scope_granted" {
			continue
		}
		grant := struct {
			Scope string `json:"scope"`
		}{}
		if json.Unmarshal([]byte(taskEvent.Body), &grant) == nil && strings.TrimSpace(grant.Scope) == requestedScope {
			return true
		}
	}
	return false
}

func (gate *Gate) recordedDecision(taskRunID string, approvalRequest mcpserver.ApprovalRequest) (mcpserver.ApprovalDecision, bool) {
	if taskRunID == "" {
		return "", false
	}
	decision := mcpserver.ApprovalDecision("")
	isDecided := false
	heldCallKey := ""
	decidedCallKey := ""
	for _, taskEvent := range gate.taskRunService.ListTaskEvent(taskRunID) {
		switch taskEvent.Name {
		case "approval.pending_call":
			heldCall := decodeHeldCallEventBody(taskEvent.Body)
			heldCallKey = canonicalToolCallKey(heldCall.ToolName, heldCall.ToolInput)
		case "approval.decided":
			decision, isDecided = decisionFromEventBody(taskEvent.Body)
			decidedCallKey = heldCallKey
		case "approval.executed":
			if executedToolName(taskEvent.Body) == strings.TrimSpace(approvalRequest.ToolName) {
				decision, isDecided = "", false
			}
		}
	}
	if !isDecided || decidedCallKey != canonicalToolCallKey(approvalRequest.ToolName, approvalRequest.ToolInput) {
		return "", false
	}
	return decision, true
}

func canonicalToolCallKey(toolName string, toolInput json.RawMessage) string {
	return strings.TrimSpace(toolName) + "\x00" + canonicalToolInput(toolInput)
}

func canonicalToolInput(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return "{}"
	}
	var document any
	if json.Unmarshal(toolInput, &document) != nil {
		return strings.TrimSpace(string(toolInput))
	}
	canonicalDocument, _ := json.Marshal(document)
	return string(canonicalDocument)
}

func decisionFromEventBody(body string) (mcpserver.ApprovalDecision, bool) {
	decidedBody := struct {
		Decision string `json:"decision"`
	}{}
	if json.Unmarshal([]byte(body), &decidedBody) != nil {
		return "", false
	}
	switch strings.TrimSpace(decidedBody.Decision) {
	case "confirm", "confirm_task":
		return mcpserver.ApprovalDecisionApproved, true
	case "cancel":
		return mcpserver.ApprovalDecisionRejected, true
	}
	return "", false
}

func unmarshalEventBody(body string, target any) {
	json.Unmarshal([]byte(body), target)
}

func decodeHeldCallEventBody(body string) heldCallRecord {
	decodedBody := heldCallRecord{}
	unmarshalEventBody(body, &decodedBody)
	decodedBody.ToolName = strings.TrimSpace(decodedBody.ToolName)
	return decodedBody
}

func executedToolName(body string) string {
	executedBody := struct {
		ToolName string `json:"toolName"`
	}{}
	json.Unmarshal([]byte(body), &executedBody)
	return strings.TrimSpace(executedBody.ToolName)
}

func (gate *Gate) recordHeldCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, target ApprovalTarget) {
	gate.taskRunService.AppendTaskEvent(taskRunID, "approval.pending_call", marshalEventBody(heldCallRecord{
		ToolName:          approvalRequest.ToolName,
		ToolInput:         approvalRequest.ToolInput,
		ApprovedToolInput: narrowedToolInput(approvalRequest.ToolInput, target),
		ApprovalScope:     approvalRequest.ApprovalScope,
		Confirmation:      confirmation,
		HarnessSession:    approvalRequest.HarnessSession,
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, "confirmation.requested", marshalEventBody(map[string]string{
		"userFacingMessage": confirmation,
		"message":           confirmation,
		"reasonCode":        approvalReasonCode(approvalRequest),
		"reasonDetail":      "approval gate for " + approvalRequest.ToolName,
		"responseLanguage":  approvalRequest.ResponseLanguage,
		"source":            "tool_catalog",
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, "ask.requested", marshalEventBody(askRecord(approvalRequest, confirmation)))
}

func approvalReasonCode(approvalRequest mcpserver.ApprovalRequest) string {
	if sideEffectClass := strings.TrimSpace(approvalRequest.SideEffectClass); sideEffectClass != "" {
		return sideEffectClass
	}
	return "approval_required"
}

func askRecord(approvalRequest mcpserver.ApprovalRequest, confirmation string) map[string]any {
	record := map[string]any{
		"kind":             "ask_confirm",
		"message":          confirmation,
		"reasonCode":       approvalReasonCode(approvalRequest),
		"reasonDetail":     "approval gate for " + approvalRequest.ToolName,
		"responseLanguage": approvalRequest.ResponseLanguage,
	}
	if approvalScope := strings.TrimSpace(approvalRequest.ApprovalScope); approvalScope != "" {
		record["approvalScope"] = approvalScope
		record["sessionApprovable"] = true
		record["actions"] = []string{"confirm", "confirm_task", "cancel"}
	}
	return record
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}
