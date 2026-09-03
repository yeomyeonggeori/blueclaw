package launchfailure

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type Completer struct {
	taskRunService taskstate.TaskRunStore
	languageModel  model.LanguageModelProvider
}

func NewCompleter(taskRunService taskstate.TaskRunStore, languageModel model.LanguageModelProvider) *Completer {
	return &Completer{taskRunService: taskRunService, languageModel: languageModel}
}

func (completer *Completer) CompleteLaunchFailure(responseContext context.Context, request agentcontract.AgentTurnRequest, phase string, stepName string, errorValue error) agentcontract.AgentTurnResult {
	taskRun, createError := completer.taskRunForLaunchFailure(request)
	reason := firstNonEmptyString(errorText(errorValue), errorText(createError))
	if createError != nil {
		reason = strings.TrimSpace(reason + "; task_run_create=" + createError.Error())
	}
	failedTaskRun, failError := completer.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	if failError != nil {
		taskRun.Status = agentcontract.TaskStatusFailed
		taskRun.FailureReason = firstNonEmptyString(reason, failError.Error())
		failedTaskRun = taskRun
	}
	launchFailureReport := agentcontract.FailureReport{
		Phase:              phase,
		StepName:           stepName,
		StopReason:         reason,
		SafeFailureSummary: reason,
		RawError:           reason,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  agentcontract.DiagnosticEventID(request, taskRun.TaskRunID, phase),
	}
	failureNotice, noticeStatus := (agentcontract.FailureNoticeGenerator{LanguageModel: completer.languageModel}).Generate(responseContext, launchFailureReport)
	completer.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReply, marshalEventBody(noticeStatus))
	completer.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReport, marshalEventBody(map[string]any{
		"phase":      phase,
		"report":     launchFailureReport,
		"generation": noticeStatus,
	}))
	failedTaskRun = persistTaskRunResult(completer.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	return agentcontract.AgentTurnResult{
		TaskRun:       failedTaskRun,
		UserNotice:    failedTaskRun.Result,
		FailureNotice: failureNotice,
		ToolNames:     toolNamesForEvent(request.ToolSet),
	}
}

func (completer *Completer) taskRunForLaunchFailure(request agentcontract.AgentTurnRequest) (agentcontract.TaskRun, error) {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := completer.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun, nil
		}
	}
	return completer.taskRunService.CreateTaskRunWithOriginAndError(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func persistTaskRunResult(taskRunService taskstate.TaskRunStore, taskRun agentcontract.TaskRun, result string) agentcontract.TaskRun {
	persistedTaskRun, errorValue := taskRunService.RecordTaskRunResult(taskRun.TaskRunID, result)
	if errorValue != nil {
		taskRun.Result = result
		return taskRun
	}
	return persistedTaskRun
}

func toolNamesForEvent(toolSet *toolcontract.ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListToolNames()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func errorText(errorValue error) string {
	if errorValue == nil {
		return ""
	}
	return errorValue.Error()
}

func marshalEventBody(value any) string {
	body, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(body)
}

type IntakeLimit struct {
	TaskLevel         string
	MaxIterationCount int
	MaxToolCallCount  int
	MaxElapsedSecond  int
	TurnStartedAt     time.Time
	WorkDeadline      time.Time
}

func (completer *Completer) CompleteIntakeElapsed(responseContext context.Context, request agentcontract.AgentTurnRequest, intakeLimit IntakeLimit) agentcontract.AgentTurnResult {
	taskRun := completer.taskRunForIntakeLimit(request)
	completer.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentLimitStop, marshalEventBody(intakeLimitEventBody(intakeLimit)))
	blockedTaskRun, errorValue := completer.taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusBlocked, "max_elapsed")
	if errorValue != nil {
		taskRun.Status = agentcontract.TaskStatusBlocked
		taskRun.FailureReason = "max_elapsed"
		blockedTaskRun = taskRun
	}
	failureNotice, noticeStatus := (agentcontract.FailureNoticeGenerator{LanguageModel: completer.languageModel}).Generate(responseContext, agentcontract.FailureReport{
		Phase:              "limit",
		StopReason:         "max_elapsed",
		SafeFailureSummary: agentcontract.ElapsedLimitRawErrorSummary,
		RawError:           agentcontract.ElapsedLimitRawErrorSummary,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  taskRun.TaskRunID + ":intake_limit",
	})
	completer.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentLimitReply, marshalEventBody(map[string]any{
		"source":            noticeStatus.Source,
		"reason":            noticeStatus.Reason,
		"textRecoveryError": noticeStatus.TextRecoveryError,
	}))
	blockedTaskRun = persistTaskRunResult(completer.taskRunService, blockedTaskRun, failureNotice.SendableMessage())
	completer.taskRunService.AppendTaskEvent(blockedTaskRun.TaskRunID, agentcontract.TaskEventAgentGoalBlocked, marshalEventBody(agentcontract.ActiveGoal{
		GoalID:              blockedTaskRun.TaskRunID,
		TaskRunID:           blockedTaskRun.TaskRunID,
		OriginalInstruction: strings.TrimSpace(request.Prompt),
		Status:              agentcontract.ActiveGoalStatusBlocked,
	}))
	return agentcontract.AgentTurnResult{
		TaskRun:       blockedTaskRun,
		UserNotice:    failureNotice.SendableMessage(),
		FailureNotice: failureNotice,
		ToolNames:     toolNamesForEvent(request.ToolSet),
	}
}

func (completer *Completer) taskRunForIntakeLimit(request agentcontract.AgentTurnRequest) agentcontract.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := completer.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	taskRun, _ := completer.taskRunService.CreateTaskRunWithOriginAndError(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
	return taskRun
}

func intakeLimitEventBody(intakeLimit IntakeLimit) map[string]any {
	body := map[string]any{
		"phase":              "intake",
		"taskLevel":          intakeLimit.TaskLevel,
		"maxIterationCount":  intakeLimit.MaxIterationCount,
		"maxElapsedSecond":   intakeLimit.MaxElapsedSecond,
		"maxToolCallCount":   intakeLimit.MaxToolCallCount,
		"usedIterationCount": 0,
		"usedToolCallCount":  0,
		"limitStopReason":    "max_elapsed",
		"anchorClamped":      false,
		"nowUnixMs":          time.Now().UnixMilli(),
	}
	if !intakeLimit.TurnStartedAt.IsZero() {
		body["turnStartedAtUnixMs"] = intakeLimit.TurnStartedAt.UnixMilli()
	}
	if !intakeLimit.WorkDeadline.IsZero() {
		body["workDeadlineUnixMs"] = intakeLimit.WorkDeadline.UnixMilli()
	}
	return body
}
