package approvalgate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type Gate struct {
	taskRunService         taskstate.TaskRunStore
	questionWorder         holdrecord.QuestionWorder
	approvalTargetResolver ApprovalTargetResolver
	questionPoster         QuestionPoster
	schedules              Schedules
}

func New(taskRunService taskstate.TaskRunStore) *Gate {
	return &Gate{taskRunService: taskRunService}
}

func (gate *Gate) AwaitApproval(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) (mcpserver.ApprovalOutcome, error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	if _, isApproved := gate.approvedHold(taskRunID, approvalRequest); isApproved {
		return gate.approvedOutcome(taskRunID, approvalRequest), nil
	}
	resolution, confirmation, isQuestion := gate.questionToAsk(approvalRequest)
	if !isQuestion {
		resolution = gate.resolveApprovalTarget(ctx, approvalRequest)
		if resolution.namesNothingThatExists() {
			return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnresolvedTarget, Failure: resolution.Failure}, nil
		}
		if sendReachesOnlyTheRequester(approvalRequest, resolution.Target) {
			return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved}, nil
		}
		if gate.isAnsweredByItsSchedule(approvalRequest) {
			return gate.approvedByItsSchedule(taskRunID, approvalRequest, resolution), nil
		}
		confirmation = gate.confirmationWording(ctx, approvalRequest, resolution)
	}
	return gate.parkedOutcome(ctx, taskRunID, approvalRequest, heldCallOf(approvalRequest, confirmation, resolution), resolution.Choices), nil
}

func (gate *Gate) holdCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, heldCall agentcontract.HeldCall, choices []holdrecord.Choice) (holdrecord.Hold, bool) {
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, heldCall.Confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return holdrecord.Hold{}, false
	}
	return gate.recordHeldCall(taskRunID, approvalRequest, heldCall, choices), true
}

func (gate *Gate) approvedOutcome(taskRunID string, approvalRequest mcpserver.ApprovalRequest) mcpserver.ApprovalOutcome {
	holdID := RecordApprovalSpent(gate.taskRunService, taskRunID, approvalRequest.ToolName, approvalRequest.ToolInput)
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved, HoldID: holdID}
}

func (gate *Gate) approvedHold(taskRunID string, approvalRequest mcpserver.ApprovalRequest) (holdrecord.Hold, bool) {
	if taskRunID == "" {
		return holdrecord.Hold{}, false
	}
	return holdrecord.ApprovedHoldForCall(holdrecord.Holds(gate.taskRunService.ListTaskEvent(taskRunID)), approvalRequest.ToolName, approvalRequest.ToolInput)
}

func heldCallOf(approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) agentcontract.HeldCall {
	return agentcontract.HeldCall{
		ToolName:          approvalRequest.ToolName,
		ToolInput:         approvalRequest.ToolInput,
		ApprovedToolInput: narrowedToolInput(approvalRequest.ToolInput, resolution.Target),
		Confirmation:      confirmation,
		HarnessSession:    approvalRequest.HarnessSession,
		HandoffID:         resolution.Target.HandoffID,
	}
}

func (gate *Gate) recordHeldCall(taskRunID string, approvalRequest mcpserver.ApprovalRequest, heldCall agentcontract.HeldCall, choices []holdrecord.Choice) holdrecord.Hold {
	hold := holdrecord.Open(gate.taskRunService, taskRunID, heldCall, choices)
	gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConfirmationRequested, marshalEventBody(map[string]string{
		"userFacingMessage": heldCall.Confirmation,
		"message":           heldCall.Confirmation,
		"reasonCode":        approvalReasonCode(approvalRequest),
		"reasonDetail":      "approval gate for " + approvalRequest.ToolName,
		"responseLanguage":  approvalRequest.ResponseLanguage,
		"source":            "tool_catalog",
	}))
	gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, marshalEventBody(askRecord(approvalRequest, heldCall.Confirmation)))
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
