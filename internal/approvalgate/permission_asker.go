package approvalgate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type PermissionAsker interface {
	AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question PermissionQuestion) (ApprovalAnswer, bool)
}

type PermissionQuestion struct {
	Confirmation string
	Choices      []ApprovalChoice
}

func (gate *Gate) UsePermissionAsker(permissionAsker PermissionAsker) {
	gate.permissionAsker = permissionAsker
}

func (gate *Gate) askedOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, resolution ApprovalTargetResolution) (mcpserver.ApprovalOutcome, bool) {
	if gate.permissionAsker == nil || taskRunID == "" {
		return mcpserver.ApprovalOutcome{}, false
	}
	profileName := gate.currentAgentProfileName(taskRunID)
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}, true
	}
	gate.recordHeldCall(taskRunID, approvalRequest, confirmation, resolution)

	answer, isAnswered := gate.permissionAsker.AskPermission(ctx, approvalRequest, PermissionQuestion{Confirmation: confirmation, Choices: resolution.Choices})
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
	gate.mintHeldCallApproval(taskRunID, approvalRequest)
	RecordRequesterDecision(gate.taskRunService, taskRunID, &answer.Signal, "acp_permission")
	if answer.Signal == agentcontract.ApprovalSignalReject {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionRejected}, true
	}
	if answer.Signal == agentcontract.ApprovalSignalApproveTask {
		gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalScopeGranted, marshalEventBody(map[string]string{
			"scope": approvalRequest.ApprovalScope,
		}))
	}
	return gate.approvedOutcome(taskRunID, approvalRequest), true
}

func (gate *Gate) deferredOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution, choice ApprovalChoice) mcpserver.ApprovalOutcome {
	deferral, errorValue := gate.DeferApprovedCall(ctx, DeferralRequest{
		TaskRunID:         taskRunID,
		ToolName:          approvalRequest.ToolName,
		ToolInput:         firstNonEmptyInput(narrowedToolInput(approvalRequest.ToolInput, resolution.Target), approvalRequest.ToolInput),
		RequesterPersonID: approvalRequest.RequesterPersonID,
		Platform:          approvalRequest.Platform,
		ConversationID:    approvalRequest.ConversationID,
		ReplyTargetID:     approvalRequest.ReplyTargetID,
		Prompt:            approvalRequest.Prompt,
		Choice:            choice,
		ReferenceTime:     time.Now().UTC(),
	})
	if errorValue != nil {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionDeferred, Deferral: DeferralFailedResult(errorValue)}
	}
	return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionDeferred, Deferral: deferral}
}

func DeferralFailedResult(errorValue error) toolcontract.ToolResult {
	return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "approval", "The requester chose to run this call later, and it could not be scheduled, so nothing will run: "+errorValue.Error())
}

func firstNonEmptyInput(inputs ...json.RawMessage) json.RawMessage {
	for _, input := range inputs {
		if len(input) > 0 {
			return input
		}
	}
	return nil
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
