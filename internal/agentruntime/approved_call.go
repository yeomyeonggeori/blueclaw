package agentruntime

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
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
	if approvedHold, isWaitingToRun := approvedHoldNotYetRun(execution); isWaitingToRun {
		return append(settledCalls, step.carryOutApprovedHold(ctx, execution, approvedHold)), nil
	}
	return settledCalls, nil
}

func approvedHoldNotYetRun(execution *taskLaunchExecution) (holdrecord.Hold, bool) {
	taskRunID := strings.TrimSpace(execution.Request.ExistingTaskRunID)
	if taskRunID == "" || execution.Launcher.taskRunService == nil {
		return holdrecord.Hold{}, false
	}
	holds := holdrecord.Holds(execution.Launcher.taskRunService.ListTaskEvent(taskRunID))
	if len(holds) == 0 {
		return holdrecord.Hold{}, false
	}
	latestHold := holds[len(holds)-1]
	return latestHold, latestHold.State == holdrecord.StateApproved && strings.TrimSpace(latestHold.Call.ToolName) != ""
}

func (step carryOutApprovedCallLaunchStep) carryOutApprovedHold(ctx context.Context, execution *taskLaunchExecution, approvedHold holdrecord.Hold) agentcontract.CarriedOutCall {
	approvedCall := approvalgate.ApprovedCall{ToolName: approvedHold.Call.ToolName, ToolInput: approvedHold.Call.ToolInput}
	result := invokeApprovedCall(toolcontract.WithTaskRunID(ctx, strings.TrimSpace(execution.Request.ExistingTaskRunID)), step.ToolSet, approvedCall)
	return agentcontract.CarriedOutCall{ToolName: approvedCall.ToolName, ToolInput: approvedCall.ToolInput, HoldID: approvedHold.ID, Result: result}
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
	result, errorValue := toolSet.WithAdditionalAllowedToolNames([]string{approvedCall.ToolName}).AllowingInternalTool(approvedCall.ToolName).Invoke(ctx, toolcontract.ToolInvocation{
		ToolName: approvedCall.ToolName,
		Input:    approvedCall.ToolInput,
	})
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "approval", errorValue.Error())
	}
	return result
}
