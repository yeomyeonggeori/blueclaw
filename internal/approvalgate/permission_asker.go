package approvalgate

import (
	"context"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type PermissionAsker interface {
	AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, confirmation string) (agentcontract.ApprovalSignal, bool)
}

func (gate *Gate) UsePermissionAsker(permissionAsker PermissionAsker) {
	gate.permissionAsker = permissionAsker
}

func (gate *Gate) askedOutcome(ctx context.Context, taskRunID string, approvalRequest mcpserver.ApprovalRequest, confirmation string, target ApprovalTarget) (mcpserver.ApprovalOutcome, bool) {
	if gate.permissionAsker == nil || taskRunID == "" {
		return mcpserver.ApprovalOutcome{}, false
	}
	profileName := gate.currentAgentProfileName(taskRunID)
	if _, errorValue := gate.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, confirmation); errorValue != nil {
		slog.Warn("approvalgate.call_is_unanswerable", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionUnanswerable}, true
	}
	gate.recordHeldCall(taskRunID, approvalRequest, confirmation, target)

	approvalSignal, isAnswered := gate.permissionAsker.AskPermission(ctx, approvalRequest, confirmation)
	if !isAnswered {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, true
	}
	if _, errorValue := gate.taskRunService.AdvanceTaskRun(taskRunID, profileName); errorValue != nil {
		slog.Warn("approvalgate.answered_run_will_not_advance", "taskRunID", taskRunID, "toolName", strings.TrimSpace(approvalRequest.ToolName), "reason", errorValue.Error())
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld, Notice: confirmation}, true
	}
	gate.mintHeldCallApproval(taskRunID, approvalRequest)
	RecordRequesterDecision(gate.taskRunService, taskRunID, &approvalSignal, "acp_permission")
	if approvalSignal == agentcontract.ApprovalSignalReject {
		return mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionRejected}, true
	}
	if approvalSignal == agentcontract.ApprovalSignalApproveTask {
		gate.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalScopeGranted, marshalEventBody(map[string]string{
			"scope": approvalRequest.ApprovalScope,
		}))
	}
	return gate.approvedOutcome(taskRunID, approvalRequest), true
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
