package agentruntime

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
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
	if carriedOutCall, isCarriedOut := step.carryOutAnsweredCall(ctx, execution); isCarriedOut {
		return append(settledCalls, carriedOutCall), nil
	}
	return settledCalls, nil
}

func (step carryOutApprovedCallLaunchStep) carryOutAnsweredCall(ctx context.Context, execution *taskLaunchExecution) (agentcontract.CarriedOutCall, bool) {
	taskRunID := strings.TrimSpace(execution.Request.ExistingTaskRunID)
	taskRunService := execution.Launcher.taskRunService
	if !execution.Request.IsApprovalContinuation || taskRunID == "" || taskRunService == nil {
		return agentcontract.CarriedOutCall{}, false
	}
	approvedCall, isApproved := approvalgate.ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID))
	if !isApproved {
		return agentcontract.CarriedOutCall{}, false
	}
	result := step.carriedOutResult(ctx, taskRunService.ListTaskEvent(taskRunID), approvedCall)
	approvalgate.RecordApprovedCallSpent(taskRunService, taskRunID, approvedCall)
	return agentcontract.CarriedOutCall{ToolName: approvedCall.ToolName, ToolInput: approvedCall.ToolInput, Result: result}, true
}

func (step carryOutApprovedCallLaunchStep) carriedOutResult(ctx context.Context, taskEvents []agentcontract.TaskEvent, approvedCall approvalgate.ApprovedCall) toolcontract.ToolResult {
	if approvedCall.ToolName == toolcontract.AskInputToolName {
		return approvalrecord.ChosenAnswerResult(taskEvents, approvedCall.ToolInput)
	}
	return invokeApprovedCall(ctx, step.ToolSet, approvedCall)
}

func (step carryOutApprovedCallLaunchStep) carryOutScheduledCall(ctx context.Context, execution *taskLaunchExecution, scheduledCall task.ScheduleApprovedCall) agentcontract.CarriedOutCall {
	approvedCall := approvalgate.ApprovedCall{ToolName: strings.TrimSpace(scheduledCall.ToolName), ToolInput: scheduledCall.ToolInput}
	result := invokeApprovedCall(withScheduledApprovedCall(ctx, scheduledCall), step.ToolSet, approvedCall)
	if taskRunID := strings.TrimSpace(execution.Request.ExistingTaskRunID); taskRunID != "" && execution.Launcher.taskRunService != nil {
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

type scheduledApprovedCallKey struct{}

func withScheduledApprovedCall(ctx context.Context, scheduledCall task.ScheduleApprovedCall) context.Context {
	return context.WithValue(ctx, scheduledApprovedCallKey{}, scheduledCall)
}

func scheduledApprovedCallFrom(ctx context.Context) (task.ScheduleApprovedCall, bool) {
	scheduledCall, isCarried := ctx.Value(scheduledApprovedCallKey{}).(task.ScheduleApprovedCall)
	return scheduledCall, isCarried
}
