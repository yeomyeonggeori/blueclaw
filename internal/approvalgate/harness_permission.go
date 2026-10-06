package approvalgate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const harnessPermissionSource = "harness_permission"

type HarnessPermissionQuestion struct {
	Text     string
	ToolCall acp.ToolCallUpdate
	Options  []acp.PermissionOption
}

type HarnessPermissionAsker interface {
	AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question HarnessPermissionQuestion) (acp.RequestPermissionOutcome, AskStatus)
}

func (gate *Gate) AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question HarnessPermissionQuestion) (acp.RequestPermissionOutcome, AskStatus) {
	harnessAsker, canAskHarnessPermission := gate.permissionAsker.(HarnessPermissionAsker)
	if !canAskHarnessPermission {
		return acp.RequestPermissionOutcome{}, AskUnreachable
	}
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	if taskRunID == "" {
		return harnessAsker.AskHarnessPermission(ctx, approvalRequest, question)
	}
	heldCall := heldCallOfHarnessQuestion(question)
	if outcome, isApproved := gate.spendApprovedHarnessCall(taskRunID, heldCall, question.Options); isApproved {
		return outcome, AskAnswered
	}
	profileName := gate.currentAgentProfileName(taskRunID)
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, question.Text); errorValue != nil {
		slog.Warn("approvalgate.harness_permission_is_unanswerable", "taskRunID", taskRunID, "reason", errorValue.Error())
		return acp.RequestPermissionOutcome{}, AskUnreachable
	}
	hold := holdrecord.Open(gate.taskRunService, taskRunID, heldCall, nil)
	outcome, status := harnessAsker.AskHarnessPermission(ctx, approvalRequest, question)
	switch status {
	case AskAnswered:
		gate.settleHarnessHold(taskRunID, hold.ID, question.Options, outcome)
	case AskUnreachable, AskExpired:
		gate.endUnansweredHold(taskRunID, profileName, status)
		return acp.RequestPermissionOutcome{}, status
	default:
		return acp.RequestPermissionOutcome{}, status
	}
	if _, errorValue := gate.taskRunService.AdvanceTaskRun(taskRunID, profileName); errorValue != nil {
		slog.Warn("approvalgate.answered_run_will_not_advance", "taskRunID", taskRunID, "reason", errorValue.Error())
	}
	return outcome, AskAnswered
}

func heldCallOfHarnessQuestion(question HarnessPermissionQuestion) agentcontract.HeldCall {
	heldCall := agentcontract.HeldCall{Confirmation: question.Text}
	if question.ToolCall.RawInput != nil {
		heldCall.ToolInput, _ = json.Marshal(question.ToolCall.RawInput)
	}
	return heldCall
}

func (gate *Gate) spendApprovedHarnessCall(taskRunID string, heldCall agentcontract.HeldCall, options []acp.PermissionOption) (acp.RequestPermissionOutcome, bool) {
	allowOnce, isOffered := optionOfKind(options, acp.PermissionOptionKindAllowOnce)
	if !isOffered {
		return acp.RequestPermissionOutcome{}, false
	}
	if _, isSpent := holdrecord.SpendApprovedCall(gate.taskRunService, taskRunID, heldCall.ToolName, heldCall.ToolInput); !isSpent {
		return acp.RequestPermissionOutcome{}, false
	}
	return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: allowOnce.OptionId}}, true
}

func (gate *Gate) settleHarnessHold(taskRunID string, holdID string, options []acp.PermissionOption, outcome acp.RequestPermissionOutcome) {
	if outcome.Selected == nil || !isAllowOption(options, outcome.Selected.OptionId) {
		holdrecord.Decide(gate.taskRunService, taskRunID, holdID, holdrecord.DecisionReject, harnessPermissionSource)
		return
	}
	holdrecord.Decide(gate.taskRunService, taskRunID, holdID, holdrecord.DecisionApprove, harnessPermissionSource)
}

func optionOfKind(options []acp.PermissionOption, kind acp.PermissionOptionKind) (acp.PermissionOption, bool) {
	for _, permissionOption := range options {
		if permissionOption.Kind == kind {
			return permissionOption, true
		}
	}
	return acp.PermissionOption{}, false
}

func isAllowOption(options []acp.PermissionOption, optionID acp.PermissionOptionId) bool {
	for _, permissionOption := range options {
		if permissionOption.OptionId == optionID {
			return permissionOption.Kind == acp.PermissionOptionKindAllowOnce || permissionOption.Kind == acp.PermissionOptionKindAllowAlways
		}
	}
	return false
}
