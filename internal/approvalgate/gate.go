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
	permissionAsker        PermissionAsker
	approvedCallScheduler  ApprovedCallScheduler
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
	if _, isApproved := gate.approvedHold(taskRunID, approvalRequest); isApproved {
		return gate.approvedOutcome(taskRunID, approvalRequest), nil
	}
	resolution := gate.resolveApprovalTarget(ctx, approvalRequest)
	if resolution.namesNothingThatExists() {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnresolvedTarget, Failure: resolution.Failure}, nil
	}
	confirmation := gate.confirmationWording(ctx, approvalRequest, resolution)
	if outcome, isAnswered := gate.askedOutcome(ctx, taskRunID, approvalRequest, confirmation, resolution); isAnswered {
		return outcome, nil
	}
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}, nil
	}
	gate.recordHeldCall(taskRunID, approvalRequest, confirmation, resolution)
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, nil
}

func (gate *Gate) approvedOutcome(taskRunID string, approvalRequest mcpserver.ApprovalRequest) mcpserver.ApprovalOutcome {
	approvalToken := RecordApprovalSpent(gate.taskRunService, taskRunID, approvalRequest.ToolName, approvalRequest.ToolInput)
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved, ApprovedCallID: approvalToken}
}

func (gate *Gate) taskHasApprovedScope(taskRunID string, approvalScope string) bool {
	requestedScope := strings.TrimSpace(approvalScope)
	if taskRunID == "" || requestedScope == "" {
		return false
	}
	for _, taskEvent := range gate.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != agentcontract.TaskEventApprovalScopeGranted {
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

func (gate *Gate) approvedHold(taskRunID string, approvalRequest mcpserver.ApprovalRequest) (hold, bool) {
	if taskRunID == "" {
		return hold{}, false
	}
	return approvedHoldForCall(holdsOf(gate.taskRunService.ListTaskEvent(taskRunID)), approvalRequest.ToolName, approvalRequest.ToolInput)
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

func (gate *Gate) recordHeldCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) string {
	holdID := newHoldID()
	gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalPendingCall, marshalEventBody(heldCallRecord{
		HoldID: holdID,
		HeldCall: agentcontract.HeldCall{
			ToolName:          approvalRequest.ToolName,
			ToolInput:         approvalRequest.ToolInput,
			ApprovedToolInput: narrowedToolInput(approvalRequest.ToolInput, resolution.Target),
			ApprovalScope:     approvalRequest.ApprovalScope,
			Confirmation:      confirmation,
			HarnessSession:    approvalRequest.HarnessSession,
		},
	}))
	if len(resolution.Choices) > 0 {
		gate.taskRunService.AppendTaskEvent(taskRunID, TaskEventApprovalChoicesOffered, offeredChoicesBody(approvalRequest.ToolName, approvalRequest.ToolInput, resolution.Choices))
	}
	gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConfirmationRequested, marshalEventBody(map[string]string{
		"userFacingMessage": confirmation,
		"message":           confirmation,
		"reasonCode":        approvalReasonCode(approvalRequest),
		"reasonDetail":      "approval gate for " + approvalRequest.ToolName,
		"responseLanguage":  approvalRequest.ResponseLanguage,
		"source":            "tool_catalog",
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, marshalEventBody(askRecord(approvalRequest, confirmation)))
	return holdID
}

func approvalReasonCode(approvalRequest mcpserver.ApprovalRequest) string {
	if sideEffectClass := strings.TrimSpace(approvalRequest.SideEffectClass); sideEffectClass != "" {
		return sideEffectClass
	}
	return "approval_required"
}

func askRecord(approvalRequest mcpserver.ApprovalRequest, confirmation string) map[string]any {
	return map[string]any{
		"kind":             "ask_confirm",
		"message":          confirmation,
		"reasonCode":       approvalReasonCode(approvalRequest),
		"reasonDetail":     "approval gate for " + approvalRequest.ToolName,
		"responseLanguage": approvalRequest.ResponseLanguage,
	}
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}
