package connectors

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type busyMessageResult struct {
	connectorResult ConnectorRuntimeResult
	isHandled       bool
	clearActiveGoal bool
}

func (connectorRuntime *ConnectorRuntime) handleBusyMessageIfNeeded(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	activeTaskRun, isFound := connectorRuntime.latestCurrentConversationActiveTask(personID, event.ConversationID)
	if !isFound {
		return connectorRuntime.handlePossibleFinishedTaskFollowUp(ctx, platform, event, replyTarget, personID, sendReply)
	}
	decision, errorValue := connectorRuntime.planTurn(ctx, activeTaskRun.TaskRunID, agentcontract.AgentRequest{
		RequesterPersonID: personID,
		ConversationID:    event.ConversationID,
		Prompt:            event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(event),
		VisibleContext:    event.Context.ToAgentVisibleContext(),
		ActiveTask:        connectorRuntime.activeTaskContext(activeTaskRun),
		TurnStartedAt:     time.Now(),
	})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, "task.busy_message.routed", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"busyRoute":       string(decision.BusyRoute),
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	switch decision.BusyRoute {
	case agentcontract.BusyRouteStatus:
		return connectorRuntime.handleBusyStatusMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case agentcontract.BusyRouteSteer:
		return connectorRuntime.handleBusySteerMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case agentcontract.BusyRouteReplace:
		connectorRuntime.replaceBusyTask(event, activeTaskRun, decision)
		return busyMessageResult{clearActiveGoal: true}, nil
	case agentcontract.BusyRouteCancel:
		return connectorRuntime.handleBusyCancelMessage(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	case agentcontract.BusyRouteNewTask:
		connectorRuntime.supersedeBusyTask(event, platform, activeTaskRun, decision)
		return busyMessageResult{clearActiveGoal: true}, nil
	case agentcontract.BusyRouteUnrelated:
		return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, Ignored: true, Reason: "busy_unrelated"}, isHandled: true}, nil
	default:
		return busyMessageResult{}, errors.New("turn router returned an invalid busy route")
	}
}

func (connectorRuntime *ConnectorRuntime) handleBusyCancelMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision agentcontract.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "task cancelled by newer user instruction")
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, "task.cancel.requested", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "cancel", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindUserNotice})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_cancel", ReplyDispatchID: dispatchID}, isHandled: true, clearActiveGoal: true}, nil
}

func (connectorRuntime *ConnectorRuntime) handleBusyStatusMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision agentcontract.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, "task.status.requested", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"reason":    strings.TrimSpace(decision.Reason),
	}))
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "status", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindCheckpoint})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_status", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) handleBusySteerMessage(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision agentcontract.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	instruction := firstNonEmptyString(strings.TrimSpace(decision.BusyInstruction), strings.TrimSpace(event.Prompt))
	if !connectorRuntime.taskRunService.IsTaskRunActuallyRunning(activeTaskRun) {
		return connectorRuntime.resumePausedTaskForSteer(ctx, platform, event, replyTarget, activeTaskRun, instruction, decision, sendReply)
	}
	connectorRuntime.appendSteerRequestedEvent(activeTaskRun.TaskRunID, event, instruction, decision)
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "steer", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindCheckpoint})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_steer", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) appendSteerRequestedEvent(taskRunID string, event PlatformInboundEvent, instruction string, decision agentcontract.TurnDecision) {
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, "task.steer.requested", marshalConnectorEventBody(map[string]string{
		"messageID":   event.MessageID,
		"instruction": instruction,
		"reason":      strings.TrimSpace(decision.Reason),
	}))
}

func (connectorRuntime *ConnectorRuntime) resumePausedTaskForSteer(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	instruction string,
	decision agentcontract.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(activeTaskRun.TaskRunID)
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(activeTaskRun, taskEvents)
	adapter, adapterError := connectorRuntime.findAdapter(firstNonEmptyString(platform, launchContext.Platform))
	if !isFound || adapterError != nil {
		return connectorRuntime.replySteerResumeUnavailable(ctx, platform, event, replyTarget, activeTaskRun, decision, sendReply)
	}
	connectorRuntime.appendSteerRequestedEvent(activeTaskRun.TaskRunID, event, instruction, decision)
	launchRequest := connectorRuntime.interruptedTaskLaunchRequest(activeTaskRun, taskEvents, launchContext, event, adapter, userSteerTaskProfile(platform, activeTaskRun.TaskRunID, instruction), sendReply)
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, launchRequest)
	if errorValue != nil {
		failureTurnResult := connectorRuntime.launchFailureCompleter.CompleteLaunchFailure(ctx, agentcontract.AgentTurnRequest{
			RequesterPersonID: activeTaskRun.RequesterPersonID,
			ExistingTaskRunID: activeTaskRun.TaskRunID,
			Platform:          platform,
			ConversationID:    event.ConversationID,
			Prompt:            activeTaskRun.Prompt,
			ResponseLanguage:  event.Context.ResponseLanguage,
		}, "launch", "steer_resume", errorValue)
		connectorResult, dispatchError := connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, event), adapter.Name(), adapter, event, replyTarget, failureTurnResult, "", sendReply)
		if dispatchError != nil {
			return busyMessageResult{}, dispatchError
		}
		return busyMessageResult{connectorResult: connectorResult, isHandled: true}, nil
	}
	connectorResult, errorValue := connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, event), adapter.Name(), adapter, event, replyTarget, launchResult.TurnResult, "", sendReply)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: connectorResult, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) replySteerResumeUnavailable(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	activeTaskRun task.TaskRun,
	decision agentcontract.TurnDecision,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, "task.steer.resume_unavailable", marshalConnectorEventBody(map[string]string{
		"messageID": event.MessageID,
		"reason":    strings.TrimSpace(decision.Reason),
	}))
	reply, errorValue := connectorRuntime.generateBusyReply(ctx, event, activeTaskRun, "steer", decision)
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply, TaskRunID: activeTaskRun.TaskRunID, ReplyKind: connectorReplyKindUserNotice})
	if errorValue != nil {
		return busyMessageResult{}, errorValue
	}
	return busyMessageResult{connectorResult: ConnectorRuntimeResult{Handled: true, Platform: platform, TaskRunID: activeTaskRun.TaskRunID, Reason: "busy_steer_resume_unavailable", ReplyDispatchID: dispatchID}, isHandled: true}, nil
}

func (connectorRuntime *ConnectorRuntime) replaceBusyTask(event PlatformInboundEvent, activeTaskRun task.TaskRun, decision agentcontract.TurnDecision) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "task replaced by newer user instruction")
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, "task.replaced", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) supersedeBusyTask(event PlatformInboundEvent, platform string, activeTaskRun task.TaskRun, decision agentcontract.TurnDecision) {
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(activeTaskRun.TaskRunID, activeTaskRun.RequesterPersonID, "superseded_by_new_message")
	connectorRuntime.resolveOpenTaskWaitsForTaskRun(activeTaskRun.RequesterPersonID, platform, activeTaskRun.OriginConversationID, activeTaskRun.TaskRunID)
	connectorRuntime.taskRunService.AppendTaskEvent(activeTaskRun.TaskRunID, "task.superseded_by_message", marshalConnectorEventBody(map[string]string{
		"messageID":       event.MessageID,
		"reason":          strings.TrimSpace(decision.Reason),
		"latestUserInput": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) generateBusyReply(ctx context.Context, event PlatformInboundEvent, activeTaskRun task.TaskRun, route string, decision agentcontract.TurnDecision) (string, error) {
	prompt := strings.Join([]string{
		"Write a short user-facing reply for an in-progress task.",
		"Response language: " + responseLanguageForEvent(event),
		"Route: " + route,
		"Original task: " + strings.TrimSpace(activeTaskRun.Prompt),
		"Task status: " + string(activeTaskRun.Status),
		"Current progress: " + connectorRuntime.activeTaskEventSummary(activeTaskRun.TaskRunID),
		"Latest user message: " + strings.TrimSpace(event.Prompt),
		"Routing reason: " + strings.TrimSpace(decision.Reason),
		"Steering instruction: " + strings.TrimSpace(decision.BusyInstruction),
		"Do not expose internal event names or task IDs.",
		"Status and steer replies must not claim the task is complete. Cancel replies may say the active task has been stopped.",
	}, "\n")
	return connectorRuntime.replyGenerator.GenerateReplyWithContext(ctx, prompt, event.Context.ToAgentVisibleContext(), nil)
}

// recentlyFinishedTaskFollowUpWindow bounds how long after a task leaves active status a
// later message is still eligible to be treated as a follow-up to it, rather than a
// self-contained new request.
const recentlyFinishedTaskFollowUpWindow = 15 * time.Second

// handlePossibleFinishedTaskFollowUp covers the narrow race where the active task finished
// between when a message was flagged as worth fast-tracking and when it is actually
// classified: rather than silently falling through into new-task creation (turning a
// correction into a duplicate task) or silently dropping the message, it tells the user the
// prior task already finished and lets them decide whether to start something new.
func (connectorRuntime *ConnectorRuntime) handlePossibleFinishedTaskFollowUp(
	ctx context.Context,
	platform string,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (busyMessageResult, error) {
	finishedTaskRun, isFound := connectorRuntime.latestRecentlyFinishedConversationTask(personID, event.ConversationID)
	if !isFound {
		return busyMessageResult{}, nil
	}
	if event.RawReceivedAt.IsZero() || !event.RawReceivedAt.Before(finishedTaskRun.UpdatedAt) {
		return busyMessageResult{}, nil
	}
	isRelated, errorValue := connectorRuntime.intakeClassifier.ClassifyActiveTaskFollowUp(ctx, agentcontract.ActiveTaskFollowUpClassificationRequest{
		ActiveTaskPrompt: finishedTaskRun.Prompt,
		ActiveTaskStatus: string(finishedTaskRun.Status),
		LatestMessage:    event.Prompt,
	})
	if errorValue != nil || !isRelated {
		return busyMessageResult{}, nil
	}
	connectorRuntime.taskRunService.AppendTaskEvent(finishedTaskRun.TaskRunID, "task.busy_message.after_finish", marshalConnectorEventBody(map[string]string{
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

func (connectorRuntime *ConnectorRuntime) latestRecentlyFinishedConversationTask(personID string, conversationID string) (task.TaskRun, bool) {
	var latestTaskRun task.TaskRun
	isFound := false
	cutoff := time.Now().Add(-recentlyFinishedTaskFollowUpWindow)
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(personID) {
		if taskRun.OriginConversationID != conversationID {
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

func (connectorRuntime *ConnectorRuntime) latestCurrentConversationActiveTask(personID string, conversationID string) (task.TaskRun, bool) {
	var latestTaskRun task.TaskRun
	isFound := false
	for _, taskRun := range connectorRuntime.activeTaskRunsForPerson(personID) {
		if taskRun.OriginConversationID != conversationID {
			continue
		}
		if !isFound || taskRun.UpdatedAt.After(latestTaskRun.UpdatedAt) {
			latestTaskRun = taskRun
			isFound = true
		}
	}
	return latestTaskRun, isFound
}

func (connectorRuntime *ConnectorRuntime) activeTaskContext(taskRun task.TaskRun) agentcontract.ActiveTaskContext {
	return agentcontract.ActiveTaskContext{
		TaskRunID: taskRun.TaskRunID,
		Prompt:    taskRun.Prompt,
		Status:    string(taskRun.Status),
		Summary:   connectorRuntime.activeTaskEventSummary(taskRun.TaskRunID),
	}
}

func (connectorRuntime *ConnectorRuntime) activeTaskEventSummary(taskRunID string) string {
	events := connectorRuntime.taskRunService.ListTaskEvent(taskRunID)
	summaries := []string{}
	for index := len(events) - 1; index >= 0 && len(summaries) < 6; index-- {
		event := events[index]
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
