package approvalgate

import (
	"context"

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
	approvalSignal, isAnswered := gate.permissionAsker.AskPermission(ctx, approvalRequest, confirmation)
	if !isAnswered {
		return mcpserver.ApprovalOutcome{}, false
	}
	gate.recordHeldCall(taskRunID, approvalRequest, confirmation, target)
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
