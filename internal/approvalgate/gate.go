package approvalgate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

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
		return gate.approvedOutcome(taskRunID, approvalRequest), nil
	}
	if decision, isDecided := gate.recordedDecision(taskRunID, approvalRequest); isDecided {
		if decision == mcpserver.ApprovalDecisionApproved {
			return gate.approvedOutcome(taskRunID, approvalRequest), nil
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

func (gate *Gate) approvedOutcome(taskRunID string, approvalRequest mcpserver.ApprovalRequest) mcpserver.ApprovalOutcome {
	RecordApprovalSpent(gate.taskRunService, taskRunID, approvalRequest.ToolName, approvalRequest.ToolInput)
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved}
}

func (gate *Gate) taskHasApprovedScope(taskRunID string, approvalScope string) bool {
	requestedScope := strings.TrimSpace(approvalScope)
	if taskRunID == "" || requestedScope == "" {
		return false
	}
	for _, taskEvent := range gate.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != taskstate.TaskEventApprovalScopeGranted {
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
		case taskstate.TaskEventApprovalPendingCall:
			heldCallKey = decodeHeldCallEventBody(taskEvent.Body).CanonicalCallKey()
		case taskstate.TaskEventApprovalDecided:
			decision, isDecided = decisionFromEventBody(taskEvent.Body)
			decidedCallKey = heldCallKey
		case taskstate.TaskEventApprovalExecuted:
			if executedToolName(taskEvent.Body) == strings.TrimSpace(approvalRequest.ToolName) {
				decision, isDecided = "", false
			}
		}
	}
	if !isDecided || decidedCallKey != agentcontract.CanonicalToolCallKey(approvalRequest.ToolName, approvalRequest.ToolInput) {
		return "", false
	}
	return decision, true
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

func decodeHeldCallEventBody(body string) agentcontract.HeldCall {
	decodedBody := agentcontract.HeldCall{}
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
	gate.taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalPendingCall, marshalEventBody(agentcontract.HeldCall{
		ToolName:          approvalRequest.ToolName,
		ToolInput:         approvalRequest.ToolInput,
		ApprovedToolInput: narrowedToolInput(approvalRequest.ToolInput, target),
		ApprovalScope:     approvalRequest.ApprovalScope,
		Confirmation:      confirmation,
		HarnessSession:    approvalRequest.HarnessSession,
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventConfirmationRequested, marshalEventBody(map[string]string{
		"userFacingMessage": confirmation,
		"message":           confirmation,
		"reasonCode":        approvalReasonCode(approvalRequest),
		"reasonDetail":      "approval gate for " + approvalRequest.ToolName,
		"responseLanguage":  approvalRequest.ResponseLanguage,
		"source":            "tool_catalog",
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventAskRequested, marshalEventBody(askRecord(approvalRequest, confirmation)))
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
