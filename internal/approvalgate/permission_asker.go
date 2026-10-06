package approvalgate

import (
	"context"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type PermissionAsker interface {
	AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question PermissionQuestion) (ApprovalAnswer, bool)
}

type PermissionQuestion struct {
	HoldID       string
	Confirmation string
	Choices      []holdrecord.Choice
}

func (gate *Gate) UsePermissionAsker(permissionAsker PermissionAsker) {
	gate.permissionAsker = permissionAsker
}

func (gate *Gate) askedOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) (mcpserver.ApprovalOutcome, bool) {
	if gate.permissionAsker == nil || taskRunID == "" {
		return mcpserver.ApprovalOutcome{}, false
	}
	profileName := gate.currentAgentProfileName(taskRunID)
	hold, isHeld := gate.holdCall(taskRunID, approvalRequest, confirmation, resolution)
	if !isHeld {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}, true
	}

	answer, isAnswered := gate.permissionAsker.AskPermission(ctx, approvalRequest, PermissionQuestion{HoldID: hold.ID, Confirmation: confirmation, Choices: resolution.Choices})
	if !isAnswered {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, true
	}
	if _, errorValue := gate.taskRunService.AdvanceTaskRun(taskRunID, profileName); errorValue != nil {
		slog.Warn("approvalgate.answered_run_will_not_advance", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, true
	}
	if choice, isChosen := answer.ChosenFrom(resolution.Choices); isChosen && choice.DefersTheCall() {
		return gate.deferredOutcome(ctx, taskRunID, approvalRequest, resolution, choice), true
	}
	approvalrecord.SettleSignal(gate.taskRunService, taskRunID, &answer.Signal, "acp_permission")
	if answer.Signal == agentcontract.ApprovalSignalReject {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionRejected}, true
	}
	return gate.approvedOutcome(taskRunID, approvalRequest), true
}

func (gate *Gate) deferredOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution, choice holdrecord.Choice) mcpserver.ApprovalOutcome {
	heldCall := agentcontract.HeldCall{
		ToolName:          approvalRequest.ToolName,
		ToolInput:         approvalRequest.ToolInput,
		ApprovedToolInput: narrowedToolInput(approvalRequest.ToolInput, resolution.Target),
	}
	carriedOutCall := DeferHeldCall(ctx, gate, heldCall, DeferralRequest{
		TaskRunID:         taskRunID,
		RequesterPersonID: approvalRequest.RequesterPersonID,
		Platform:          approvalRequest.Platform,
		ConversationID:    approvalRequest.ConversationID,
		ReplyTargetID:     approvalRequest.ReplyTargetID,
		Prompt:            approvalRequest.Prompt,
		Choice:            choice,
		ReferenceTime:     time.Now().UTC(),
	})
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionDeferred, Deferral: carriedOutCall.Result}
}

func DeferralFailedResult(errorValue error) toolcontract.ToolResult {
	return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "approval", "The requester chose to run this call later, and it could not be scheduled, so nothing will run: "+errorValue.Error())
}

func (gate *Gate) currentAgentProfileName(taskRunID string) string {
	taskRun, isFound := gate.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return defaultAgentProfileName
	}
	if profileName := strings.TrimSpace(taskRun.CurrentAgentProfileName); profileName != "" {
		return profileName
	}
	return defaultAgentProfileName
}

const defaultAgentProfileName = "default"
