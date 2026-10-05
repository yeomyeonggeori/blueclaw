package approvalgate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
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
	if !gate.holdCall(taskRunID, approvalRequest, confirmation, resolution) {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}, nil
	}
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, nil
}

func (gate *Gate) holdCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) bool {
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return false
	}
	gate.recordHeldCall(taskRunID, approvalRequest, confirmation, resolution)
	return true
}

func (gate *Gate) approvedOutcome(taskRunID string, approvalRequest mcpserver.ApprovalRequest) mcpserver.ApprovalOutcome {
	holdID := RecordApprovalSpent(gate.taskRunService, taskRunID, approvalRequest.ToolName, approvalRequest.ToolInput)
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved, ApprovedCallID: holdID}
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

func (gate *Gate) approvedHold(taskRunID string, approvalRequest mcpserver.ApprovalRequest) (approvalrecord.Hold, bool) {
	if taskRunID == "" {
		return approvalrecord.Hold{}, false
	}
	return approvalrecord.ApprovedHoldForCall(approvalrecord.Holds(gate.taskRunService.ListTaskEvent(taskRunID)), approvalRequest.ToolName, approvalRequest.ToolInput)
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

func (gate *Gate) recordHeldCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) {
	approvalrecord.Open(gate.taskRunService, taskRunID, agentcontract.HeldCall{
		ToolName:          approvalRequest.ToolName,
		ToolInput:         approvalRequest.ToolInput,
		ApprovedToolInput: narrowedToolInput(approvalRequest.ToolInput, resolution.Target),
		ApprovalScope:     approvalRequest.ApprovalScope,
		Confirmation:      confirmation,
		HarnessSession:    approvalRequest.HarnessSession,
	}, resolution.Choices)
	gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConfirmationRequested, marshalEventBody(map[string]string{
		"userFacingMessage": confirmation,
		"message":           confirmation,
		"reasonCode":        approvalReasonCode(approvalRequest),
		"reasonDetail":      "approval gate for " + approvalRequest.ToolName,
		"responseLanguage":  approvalRequest.ResponseLanguage,
		"source":            "tool_catalog",
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, marshalEventBody(askRecord(approvalRequest, confirmation)))
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
