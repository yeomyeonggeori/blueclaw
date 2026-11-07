package agentruntime

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const TaskEventScheduledApprovedCallCarriedOut = "approval.scheduled_call_carried_out"

type carryOutApprovedCallLaunchStep struct {
	ToolSet *toolcontract.ToolSet
}

func (carryOutApprovedCallLaunchStep) Name() string {
	return "carry_out_approved_call"
}

func (step carryOutApprovedCallLaunchStep) Run(ctx context.Context, execution *taskLaunchExecution) ([]agentcontract.CarriedOutCall, error) {
	settledCalls := append([]agentcontract.CarriedOutCall{}, execution.Request.SettledCalls...)
	if step.ToolSet == nil {
		return settledCalls, nil
	}
	if scheduledCall := execution.Request.ScheduledApprovedCall; scheduledCall != nil {
		return append(settledCalls, step.carryOutScheduledCall(ctx, execution, *scheduledCall)), nil
	}
	return settledCalls, nil
}

func (step carryOutApprovedCallLaunchStep) carryOutScheduledCall(ctx context.Context, execution *taskLaunchExecution, scheduledCall task.ScheduleApprovedCall) agentcontract.CarriedOutCall {
	approvedCall := approvalgate.ApprovedCall{ToolName: strings.TrimSpace(scheduledCall.ToolName), ToolInput: scheduledCall.ToolInput}
	taskRunID := strings.TrimSpace(execution.Request.ExistingTaskRunID)
	result := invokeApprovedCall(toolcontract.WithTaskRunID(ctx, taskRunID), step.ToolSet, approvedCall)
	if taskRunID != "" && execution.Launcher.taskRunService != nil {
		execution.Launcher.taskRunService.AppendTaskEvent(taskRunID, TaskEventScheduledApprovedCallCarriedOut, MarshalBody(map[string]any{
			"scheduleID": execution.Request.ScheduledRun.ScheduleID,
			"toolName":   approvedCall.ToolName,
			"toolInput":  approvedCall.ToolInput,
			"isError":    result.Failure != nil,
		}))
	}
	return agentcontract.CarriedOutCall{ToolName: approvedCall.ToolName, ToolInput: approvedCall.ToolInput, Result: result}
}

func invokeApprovedCall(ctx context.Context, toolSet *toolcontract.ToolSet, approvedCall approvalgate.ApprovedCall) toolcontract.ToolResult {
	result, errorValue := toolSet.WithAdditionalAllowedToolNames([]string{approvedCall.ToolName}).Invoke(ctx, toolcontract.ToolInvocation{
		ToolName: approvedCall.ToolName,
		Input:    approvedCall.ToolInput,
	})
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "approval", errorValue.Error())
	}
	return result
}
