package approvalgate

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
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
	resolution, confirmation, isQuestion := gate.questionToAsk(approvalRequest)
	if !isQuestion {
		resolution = gate.resolveApprovalTarget(ctx, approvalRequest)
		if resolution.namesNothingThatExists() {
			return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnresolvedTarget, Failure: resolution.Failure}, nil
		}
		confirmation = gate.confirmationWording(ctx, approvalRequest, resolution)
	}
	return gate.askedOutcome(ctx, taskRunID, approvalRequest, confirmation, resolution), nil
}

func (gate *Gate) holdCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) (holdrecord.Hold, bool) {
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return holdrecord.Hold{}, false
	}
	return gate.recordHeldCall(taskRunID, approvalRequest, confirmation, resolution), true
}

func (gate *Gate) approvedOutcome(taskRunID string, approvalRequest mcpserver.ApprovalRequest) mcpserver.ApprovalOutcome {
	holdID := RecordApprovalSpent(gate.taskRunService, taskRunID, approvalRequest.ToolName, approvalRequest.ToolInput)
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved, HoldID: holdID}
}

func (gate *Gate) taskHasApprovedScope(taskRunID string, approvalScope string) bool {
	if taskRunID == "" {
		return false
	}
	return holdrecord.LedgerOf(gate.taskRunService.ListTaskEvent(taskRunID)).GrantsScope(approvalScope)
}

func (gate *Gate) approvedHold(taskRunID string, approvalRequest mcpserver.ApprovalRequest) (holdrecord.Hold, bool) {
	if taskRunID == "" {
		return holdrecord.Hold{}, false
	}
	return holdrecord.ApprovedHoldForCall(holdrecord.Holds(gate.taskRunService.ListTaskEvent(taskRunID)), approvalRequest.ToolName, approvalRequest.ToolInput)
}

func (gate *Gate) recordHeldCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) holdrecord.Hold {
	hold := holdrecord.Open(gate.taskRunService, taskRunID, agentcontract.HeldCall{
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
	return hold
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
