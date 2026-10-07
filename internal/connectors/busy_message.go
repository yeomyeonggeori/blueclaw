package connectors

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type busyMessageResult struct {
	connectorResult ConnectorRuntimeResult
	isHandled       bool
	clearActiveGoal bool
}

func (connectorRuntime *ConnectorRuntime) settleBusyDecision(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	busyRoute inboundengagement.BusyRoute,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	switch busyRoute {
	case inboundengagement.BusyRouteStatus:
		return connectorRuntime.handleBusyStatusMessage(ctx, platform, event, replyTarget, activeTaskRun, sendReply)
	case inboundengagement.BusyRouteSteer:
		return connectorRuntime.handleBusySteerMessage(ctx, platform, event, replyTarget, activeTaskRun, sendReply)
	case inboundengagement.BusyRouteReplace:
		connectorRuntime.replaceBusyTask(event, activeTaskRun)
		return busyMessageResult{clearActiveGoal: true}, nil
	case inboundengagement.BusyRouteCancel:
		return connectorRuntime.handleBusyCancelMessage(ctx, platform, event, replyTarget, activeTaskRun, sendReply)
	case inboundengagement.BusyRouteNewTask:
		connectorRuntime.supersedeBusyTask(event, activeTaskRun)
		return busyMessageResult{clearActiveGoal: true}, nil
	case inboundengagement.BusyRouteUnrelated:
		return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "busy_unrelated"}, isHandled: true}, nil
	default:
		return busyMessageResult{}, errors.New("the gateway decision returned an invalid busy route")
	}
}

func (connectorRuntime *ConnectorRuntime) handleBusyCancelMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "task cancelled by newer user instruction")
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, agentcontract.TaskEventTaskCancelRequested, agentruntime.MarshalBody(map[string]string{
		"messageID":       event.MessageID,
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	busyResult, errorValue := connectorRuntime.sendBusyReply(ctx, busyReply{platform: platform, event: event, replyTarget: replyTarget, taskRun: activeTaskRun, route: "cancel", kind: connectorReplyKindUserNotice, reason: "busy_cancel"}, sendReply)
	busyResult.clearActiveGoal = errorValue == nil
	return busyResult, errorValue
}

type busyReply struct {
	platform    string
	event       PlatformInboundEvent
	replyTarget ReplyTarget
	taskRun     task.TaskRun
	route       string
	kind        string
	reason      string
}

func (connectorRuntime *ConnectorRuntime) sendBusyReply(ctx context.Context, busy busyReply, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (busyMessageResult, error) {
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, busy.event, busy.taskRun, busy.route)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, busy.replyTarget, OutboundReply{Message: reply, TaskRunID: busy.taskRun.TaskRunID, ReplyKind: busy.kind})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: busy.platform, TaskRunID: busy.taskRun.TaskRunID, Reason: busy.reason, ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) handleBusyStatusMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, agentcontract.TaskEventTaskStatusRequested, agentruntime.MarshalBody(map[string]string{
		"messageID": event.MessageID,
	}))
	return connectorRuntime.sendBusyReply(ctx, busyReply{platform: platform, event: event, replyTarget: replyTarget, taskRun: activeTaskRun, route: "status", kind: connectorReplyKindCheckpoint, reason: "busy_status"}, sendReply)
}

func (connectorRuntime *ConnectorRuntime) handleBusySteerMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	instruction := strings.TrimSpace(event.Prompt)
	if !connectorRuntime.taskRunService.IsTaskRunActuallyRunning(activeTaskRun) {
		return connectorRuntime.resumePausedTaskForSteer(ctx, platform, event, replyTarget, activeTaskRun, instruction, sendReply)
	}
	connectorRuntime.appendSteerRequestedEvent(activeTaskRun.TaskRunID, event, instruction)
	return connectorRuntime.sendBusyReply(ctx, busyReply{platform: platform, event: event, replyTarget: replyTarget, taskRun: activeTaskRun, route: "steer", kind: connectorReplyKindCheckpoint, reason: "busy_steer"}, sendReply)
}

func (connectorRuntime *ConnectorRuntime) appendSteerRequestedEvent(taskRunID string, event PlatformInboundEvent, instruction string) {
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventTaskSteerRequested, agentruntime.MarshalBody(map[string]string{
		"messageID":   event.MessageID,
		"instruction": instruction,
	}))
}

func (connectorRuntime *ConnectorRuntime) resumePausedTaskForSteer(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	instruction string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(activeTaskRun.TaskRunID)
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(activeTaskRun, taskEvents)
	adapter, adapterError := connectorRuntime.findAdapter(firstNonEmptyString(platform, launchContext.Platform))
	if !isFound || adapterError != nil {
		return connectorRuntime.replySteerResumeUnavailable(ctx, platform, event, replyTarget, activeTaskRun, sendReply)
	}
	connectorRuntime.appendSteerRequestedEvent(activeTaskRun.TaskRunID, event, instruction)
	event = connectorRuntime.withAttachmentMaterials(ctx, adapter, event, activeTaskRun.RequesterPersonID)
	launchRequest := connectorRuntime.interruptedTaskLaunchRequest(activeTaskRun, taskEvents, launchContext, event, adapter, userSteerTaskProfile(platform, activeTaskRun.TaskRunID, instruction), sendReply)
	turnResult := connectorRuntime.launchSteeredTask(ctx, platform, event, activeTaskRun, steeredTaskLaunchRequest(launchRequest, event, instruction))
	connectorResult, errorValue := connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, event), adapter.Name(), adapter, event, replyTarget, turnResult, "", sendReply)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: connectorResult, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) launchSteeredTask(ctx context.Context, platform string, event PlatformInboundEvent, activeTaskRun task.TaskRun, launchRequest agentruntime.TaskLaunchRequest) agentcontract.AgentTurnResult {
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, launchRequest)
	if errorValue == nil {
		return launchResult.TurnResult
	}
	return connectorRuntime.completeSteerResumeLaunchFailure(ctx, platform, event, activeTaskRun, errorValue)
}

func (connectorRuntime *ConnectorRuntime) completeSteerResumeLaunchFailure(ctx context.Context, platform string, event PlatformInboundEvent, activeTaskRun task.TaskRun, errorValue error) agentcontract.AgentTurnResult {
	return connectorRuntime.launchFailureCompleter.CompleteLaunchFailure(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID: activeTaskRun.RequesterPersonID,
		ExistingTaskRunID: activeTaskRun.TaskRunID,
		Platform:          platform,
		ConversationID:    event.ConversationID,
		Prompt:            activeTaskRun.Prompt,
		ResponseLanguage:  event.Context.ResponseLanguage,
	}, "launch", "steer_resume", errorValue)
}

func steeredTaskLaunchRequest(launchRequest agentruntime.TaskLaunchRequest, event PlatformInboundEvent, instruction string) agentruntime.TaskLaunchRequest {
	steeredPrompt := firstNonEmptyString(strings.TrimSpace(event.Prompt), instruction)
	if steeredPrompt == "" {
		return launchRequest
	}
	launchRequest.Prompt = steeredPrompt
	launchRequest.IsRuntimeRestartResume = false
	return launchRequest
}

func (connectorRuntime *ConnectorRuntime) replySteerResumeUnavailable(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, agentcontract.TaskEventTaskSteerResumeUnavailable, agentruntime.MarshalBody(map[string]string{
		"messageID": event.MessageID,
	}))
	return connectorRuntime.sendBusyReply(ctx, busyReply{platform: platform, event: event, replyTarget: replyTarget, taskRun: activeTaskRun, route: "steer", kind: connectorReplyKindUserNotice, reason: "busy_steer_resume_unavailable"}, sendReply)
}

func (connectorRuntime *ConnectorRuntime) replaceBusyTask(event PlatformInboundEvent, activeTaskRun task.TaskRun) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "task replaced by newer user instruction")
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, agentcontract.TaskEventTaskReplaced, agentruntime.MarshalBody(map[string]string{
		"messageID":       event.MessageID,
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) supersedeBusyTask(event PlatformInboundEvent, activeTaskRun task.TaskRun) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "superseded_by_new_message")
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, agentcontract.TaskEventTaskSupersededByMessage, agentruntime.MarshalBody(map[string]string{
		"messageID":       event.MessageID,
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) generateBusyReply(ctx context.Context, event PlatformInboundEvent, activeTaskRun task.TaskRun, route string) (string, error) {
	prompt := strings.Join([]string{
		"Write a short user-facing reply for an in-progress task.",
		"Response language: " + responseLanguageForEvent(event),
		"Route: " + route,
		"Original task: " + strings.TrimSpace(activeTaskRun.Prompt),
		"Task status: " + string(activeTaskRun.Status),
		"Current progress: " + connectorRuntime.activeTaskEventSummary(activeTaskRun.TaskRunID),
		"Latest user message: " + strings.TrimSpace(event.Prompt),
		"Do not expose internal event names or task IDs.",
		"Status and steer replies must not claim the task is complete. Cancel replies may say the active task has been stopped.",
	}, "\n")
	return connectorRuntime.replyGenerator.GenerateReplyWithContext(ctx, prompt, event.Context.ToAgentVisibleContext(), nil)
}

const recentlyFinishedTaskFollowUpWindow = 15 * time.Second

func (connectorRuntime *ConnectorRuntime) handlePossibleFinishedTaskFollowUp(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	finishedTaskRun, isFound := connectorRuntime.latestRecentlyFinishedConversationTask(personID, event)
	if !isFound {
		return busyMessageResult{}, nil
	}
	if event.RawReceivedAt.IsZero() || !event.RawReceivedAt.Before(finishedTaskRun.UpdatedAt) {
		return busyMessageResult{}, nil
	}
	if !connectorRuntime.relatesToActiveTask(ctx, adapter, event) {
		return busyMessageResult{}, nil
	}
	connectorRuntime.taskRunService.AppendTaskEvent(finishedTaskRun.TaskRunID, agentcontract.TaskEventTaskBusyMessageAfterFinish, agentruntime.MarshalBody(map[string]string{
		"messageID":       event.MessageID,
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	reply, errorValue := connectorRuntime.generateFinishedTaskFollowUpReply(ctx, event, finishedTaskRun)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: finishedTaskRun.TaskRunID, ReplyKind: connectorReplyKindUserNotice})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: finishedTaskRun.TaskRunID, Reason: "busy_finished_followup", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) generateFinishedTaskFollowUpReply(ctx context.Context, event PlatformInboundEvent, finishedTaskRun task.TaskRun) (string, error) {
	prompt := strings.Join([]string{
		"Write a short user-facing reply. The task the user seems to be reacting to already finished before this message arrived.",
		"Response language: " + responseLanguageForEvent(event),
		"Finished task: " + strings.TrimSpace(finishedTaskRun.Prompt),
		"Final status: " + string(finishedTaskRun.Status),
		"Latest user message: " + strings.TrimSpace(event.Prompt),
		"Tell the user the task already finished before this message could apply to it, and ask whether they want it undone/redone or want to start something new. Do not silently start new work.",
	}, "\n")
	return connectorRuntime.replyGenerator.GenerateReplyWithContext(ctx, prompt, event.Context.ToAgentVisibleContext(), nil)
}

func (connectorRuntime *ConnectorRuntime) latestRecentlyFinishedConversationTask(personID string, event PlatformInboundEvent) (task.TaskRun, bool) {
	var latestTaskRun task.TaskRun
	isFound := false
	cutoff := time.Now().Add(-recentlyFinishedTaskFollowUpWindow)
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(personID) {
		if !taskRunSharesMessageThread(taskRun, event) {
			continue
		}
		if isTaskControlActiveStatus(taskRun.Status) {
			continue
		}
		if taskRun.UpdatedAt.Before(cutoff) {
			continue
		}
		if !isFound || taskRun.UpdatedAt.After(latestTaskRun.UpdatedAt) {
			latestTaskRun = taskRun
			isFound = true
		}
	}
	return latestTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) latestCurrentConversationActiveTask(personID string, event PlatformInboundEvent) (task.TaskRun, bool) {
	var latestTaskRun task.TaskRun
	isFound := false
	for _, taskRun := range connectorRuntime.activeTaskRunsForPerson(personID) {
		if !taskRunSharesMessageThread(taskRun, event) {
			continue
		}
		if !isFound || taskRun.UpdatedAt.After(latestTaskRun.UpdatedAt) {
			latestTaskRun = taskRun
			isFound = true
		}
	}
	return latestTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) latestRunningConversationTask(personID string, event PlatformInboundEvent) (task.TaskRun, bool) {
	var latestTaskRun task.TaskRun
	isFound := false
	for _, taskRun := range connectorRuntime.activeTaskRunsForPerson(personID) {
		if !taskRunSharesMessageThread(taskRun, event) || taskRun.Status == task.TaskStatusWaitingApproval || taskRun.Status == task.TaskStatusWaitingUserInput {
			continue
		}
		if !isFound || taskRun.UpdatedAt.After(latestTaskRun.UpdatedAt) {
			latestTaskRun = taskRun
			isFound = true
		}
	}
	return latestTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) taskFactsOf(taskRun task.TaskRun) inboundengagement.TaskFacts {
	return inboundengagement.TaskFacts{
		Prompt:  taskRun.Prompt,
		Status:  string(taskRun.Status),
		Summary: connectorRuntime.activeTaskEventSummary(taskRun.TaskRunID),
	}
}

var eventsThatReportNoProgress = []string{
	agentcontract.TaskEventTaskCreated,
	agentcontract.TaskEventTaskRunning,
	agentcontract.TaskEventLLMCall,
}

func (connectorRuntime *ConnectorRuntime) activeTaskEventSummary(taskRunID string) string {
	events := connectorRuntime.taskRunService.ListTaskEvent(taskRunID)
	summaries := []string{}
	for index := len(events) - 1; index >= 0 && len(summaries) < 6; index-- {
		event := events[index]
		if slices.Contains(eventsThatReportNoProgress, event.Name) {
			continue
		}
		body := strings.TrimSpace(event.Body)
		if len(body) > 240 {
			body = body[:240]
		}
		summaries = append(summaries, strings.TrimSpace(event.Name)+" "+body)
	}
	if len(summaries) == 0 {
		return "No progress events have been recorded yet."
	}
	return strings.Join(summaries, "\n")
}
