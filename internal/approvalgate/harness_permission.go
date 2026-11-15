package approvalgate

import (
	"context"
	"encoding/json"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

type HarnessPermissionQuestion struct {
	Text     string
	ToolCall acp.ToolCallUpdate
	Options  []acp.PermissionOption
}

type HarnessPermissionAsker interface {
	AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question HarnessPermissionQuestion) (outcome acp.RequestPermissionOutcome, isAnswered bool)
}

func (gate *Gate) AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question HarnessPermissionQuestion) (acp.RequestPermissionOutcome, bool) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	if outcome, isAnswered := gate.knownHarnessAnswer(taskRunID, approvalRequest, heldCallOfHarnessQuestion(question), question.Options); isAnswered {
		return outcome, true
	}
	question = gate.wordedHarnessQuestion(ctx, approvalRequest, question)
	gate.parkedOutcome(ctx, taskRunID, approvalRequest, heldCallOfHarnessQuestion(question), nil)
	return acp.RequestPermissionOutcome{}, false
}

func (gate *Gate) wordedHarnessQuestion(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question HarnessPermissionQuestion) HarnessPermissionQuestion {
	if gate.questionWorder == nil {
		return question
	}
	question.Text = gate.confirmationWording(ctx, approvalRequest, ApprovalTargetResolution{})
	return question
}

func (gate *Gate) knownHarnessAnswer(taskRunID string, approvalRequest mcpserver.ApprovalRequest, heldCall agentcontract.HeldCall, options []acp.PermissionOption) (acp.RequestPermissionOutcome, bool) {
	if taskRunID == "" {
		return acp.RequestPermissionOutcome{}, false
	}
	if outcome, isApproved := gate.spendApprovedHarnessCall(taskRunID, heldCall, options); isApproved {
		return outcome, true
	}
	if !gate.isAnsweredByItsSchedule(approvalRequest) {
		return acp.RequestPermissionOutcome{}, false
	}
	return gate.harnessAllowedByItsSchedule(taskRunID, heldCall, options)
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

func optionOfKind(options []acp.PermissionOption, kind acp.PermissionOptionKind) (acp.PermissionOption, bool) {
	for _, permissionOption := range options {
		if permissionOption.Kind == kind {
			return permissionOption, true
		}
	}
	return acp.PermissionOption{}, false
}
