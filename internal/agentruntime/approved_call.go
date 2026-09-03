package agentruntime

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type carryOutApprovedCallLaunchStep struct {
	ToolSet *toolcontract.ToolSet
}

func (carryOutApprovedCallLaunchStep) Name() string {
	return "carry_out_approved_call"
}

func (step carryOutApprovedCallLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) ([]agentcontract.CarriedOutCall, error) {
	taskRunID := strings.TrimSpace(execution.Request.ExistingTaskRunID)
	taskRunService := execution.Launcher.taskRunService
	if !execution.Request.IsApprovalContinuation || taskRunID == "" || taskRunService == nil || step.ToolSet == nil {
		return nil, nil
	}
	approvedCall, isApproved := approvalgate.ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID))
	if !isApproved {
		return nil, nil
	}
	result := invokeApprovedCall(ctx, step.ToolSet, approvedCall)
	approvalgate.RecordApprovalSpent(taskRunService, taskRunID, approvedCall.ToolName, approvedCall.ToolInput)
	return []agentcontract.CarriedOutCall{{
		ToolName:  approvedCall.ToolName,
		ToolInput: approvedCall.ToolInput,
		Result:    result,
	}}, nil
}

func invokeApprovedCall(ctx context.Context, toolSet *toolcontract.ToolSet, approvedCall approvalgate.ApprovedCall) toolcontract.ToolResult {
	carryOutToolSet := toolSet.WithAdditionalAllowedToolNames([]string{approvedCall.ToolName})
	carryOutToolSet.UseToolCallGate(nil)
	result, errorValue := carryOutToolSet.Invoke(ctx, toolcontract.ToolInvocation{
		ToolName: approvedCall.ToolName,
		Input:    approvedCall.ToolInput,
	})
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "approval", errorValue.Error())
	}
	return result
}
