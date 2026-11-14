package agentruntime

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (taskLauncher *TaskLauncher) startTurnOnTaskRun(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (context.Context, func()) {
	turnContext, cancelTurn := context.WithCancel(ctx)
	taskRunID := strings.TrimSpace(turnRequest.ExistingTaskRunID)
	if taskRunID == "" {
		return turnContext, cancelTurn
	}
	taskLauncher.taskRunService.AdvanceTaskRun(taskRunID, turnRequest.ProfileName)
	forgetTurn := taskLauncher.taskRunService.RegisterTaskRunCancel(taskRunID, cancelTurn)
	return turnContext, func() {
		forgetTurn()
		cancelTurn()
	}
}

func (taskLauncher *TaskLauncher) settledTurnResult(turnRequest agentcontract.AgentTurnRequest, turnResult agentcontract.AgentTurnResult) agentcontract.AgentTurnResult {
	taskRunID := strings.TrimSpace(turnRequest.ExistingTaskRunID)
	if taskRunID == "" || !isAboutTaskRun(turnResult, taskRunID) {
		return turnResult
	}
	storedTaskRun, isFound := taskLauncher.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return turnResult
	}
	if storedTaskRun.Status.IsAwaitingAnAnswer() {
		return awaitingTurnResult(storedTaskRun, turnResult)
	}
	taskLauncher.settleTaskRun(taskRunID, turnResult.TaskRun)
	if settledTaskRun, isFound := taskLauncher.taskRunService.FindTaskRun(taskRunID); isFound {
		turnResult.TaskRun = settledTaskRun
	}
	return turnResult
}

func isAboutTaskRun(turnResult agentcontract.AgentTurnResult, taskRunID string) bool {
	namedTaskRunID := strings.TrimSpace(turnResult.TaskRun.TaskRunID)
	return namedTaskRunID == "" || namedTaskRunID == taskRunID
}

func (taskLauncher *TaskLauncher) settleTaskRun(taskRunID string, verdict agentcontract.TaskRun) {
	switch verdict.Status {
	case agentcontract.TaskStatusCompleted:
		taskLauncher.taskRunService.CompleteTaskRun(taskRunID, verdict.Result)
	case agentcontract.TaskStatusFailed:
		taskLauncher.taskRunService.FailTaskRun(taskRunID, verdict.FailureReason)
	case agentcontract.TaskStatusBlocked, agentcontract.TaskStatusWaitingUserInput:
		taskLauncher.taskRunService.PauseTaskRun(taskRunID, verdict.Status, verdict.FailureReason)
	}
}

func awaitingTurnResult(awaitingTaskRun agentcontract.TaskRun, turnResult agentcontract.AgentTurnResult) agentcontract.AgentTurnResult {
	return agentcontract.AgentTurnResult{
		TaskRun:     awaitingTaskRun,
		UserNotice:  awaitingTaskRun.FailureReason,
		ToolNames:   turnResult.ToolNames,
		Attachments: turnResult.Attachments,
	}
}
