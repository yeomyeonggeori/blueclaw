package connectors

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type taskControlSelection struct {
	intent            agentcontract.TaskControlIntent
	reason            string
	cancelledTaskRuns []task.TaskRun
	hasNoTarget       bool
}

func (connectorRuntime *ConnectorRuntime) handleTaskControlIfRequested(
	ctx context.Context,
	platform string,
	adapter PlatformAdapter,
	event PlatformInboundEvent,
	replyTarget ReplyTarget,
	personID string,
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) (ConnectorRuntimeResult, bool) {
	decision, hasDecision := taskControlIntent(event)
	if !hasDecision || decision.Intent == agentcontract.TaskControlIntentNone {
		return ConnectorRuntimeResult{}, false
	}

	selection := connectorRuntime.applyTaskControlIntent(decision, personID, event)
	for _, taskRun := range selection.cancelledTaskRuns {
		connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskStopRequested, marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
			"intent":    string(selection.intent),
			"reason":    selection.reason,
		}))
		connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskStopClassified, marshalConnectorEventBody(map[string]any{
			"intent":             string(selection.intent),
			"reason":             selection.reason,
			"classifiedByLLM":    false,
			"originConversation": event.ConversationID,
		}))
		connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskStopCancelled, marshalConnectorEventBody(map[string]string{
			"messageID": event.MessageID,
		}))
	}

	reply := taskControlReply(selection, responseLanguageForEvent(event))
	dispatchID, errorValue := sendReply(ctx, replyTarget, OutboundReply{Message: reply})
	if errorValue != nil {
		connectorRuntime.logger.Error("connector."+platform+".task_control.reply_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
		return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "task_control_reply_failed"}, true
	}
	connectorRuntime.logger.Info("connector."+adapter.Name()+".task_control.handled", slog.String("messageID", event.MessageID), slog.String("intent", string(selection.intent)), slog.Int("cancelled", len(selection.cancelledTaskRuns)))
	return ConnectorRuntimeResult{Handled: true, Platform: platform, Reason: "task_control", ReplyDispatchID: dispatchID}, true
}

func taskControlIntent(event PlatformInboundEvent) (agentcontract.TaskControlIntentDecision, bool) {
	if intent := exactTaskControlIntent(event.Prompt); intent != agentcontract.TaskControlIntentNone {
		return agentcontract.TaskControlIntentDecision{Intent: intent, Reason: "explicit control command"}, true
	}
	return agentcontract.TaskControlIntentDecision{}, false
}

func (connectorRuntime *ConnectorRuntime) applyTaskControlIntent(decision agentcontract.TaskControlIntentDecision, personID string, event PlatformInboundEvent) taskControlSelection {
	selection := taskControlSelection{
		intent: decision.Intent,
		reason: firstNonEmptyString(decision.Reason, "user requested task stop"),
	}
	switch decision.Intent {
	case agentcontract.TaskControlIntentStopAll:
		selection.cancelledTaskRuns = connectorRuntime.taskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
			RequesterPersonID: personID,
			Reason:            selection.reason,
		})
	case agentcontract.TaskControlIntentStop:
		selection.cancelledTaskRuns = connectorRuntime.cancelLatestStopScopedTask(personID, event, selection.reason)
	}
	selection.hasNoTarget = len(selection.cancelledTaskRuns) == 0
	return selection
}

func (connectorRuntime *ConnectorRuntime) cancelLatestStopScopedTask(personID string, event PlatformInboundEvent, reason string) []task.TaskRun {
	taskRun, isFound := connectorRuntime.latestStopScopedTask(personID, event)
	if !isFound {
		return nil
	}
	return connectorRuntime.taskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
		TaskRunIDs:        []string{taskRun.TaskRunID},
		RequesterPersonID: personID,
		Reason:            reason,
	})
}

func (connectorRuntime *ConnectorRuntime) latestStopScopedTask(personID string, event PlatformInboundEvent) (task.TaskRun, bool) {
	var selectedTaskRun task.TaskRun
	isSelected := false
	for _, taskRun := range connectorRuntime.activeTaskRunsForPerson(personID) {
		if !taskRunMatchesStopScope(taskRun, event) {
			continue
		}
		if isSelected && !taskRun.UpdatedAt.After(selectedTaskRun.UpdatedAt) {
			continue
		}
		selectedTaskRun = taskRun
		isSelected = true
	}
	return selectedTaskRun, isSelected
}

func taskRunMatchesStopScope(taskRun task.TaskRun, event PlatformInboundEvent) bool {
	if taskRun.OriginConversationID != event.ConversationID {
		return false
	}
	if eventIsThreadReply(event) {
		return taskRun.OriginReplyTargetID == event.ReplyTargetID
	}
	return !taskRun.OriginIsThread
}

func (connectorRuntime *ConnectorRuntime) activeTaskRunsForPerson(personID string) []task.TaskRun {
	taskRuns := []task.TaskRun{}
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(personID) {
		if connectorRuntime.interruptInactiveRuntimeTaskIfNeeded(taskRun) {
			continue
		}
		if isTaskControlActiveStatus(taskRun.Status) {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns
}

func (connectorRuntime *ConnectorRuntime) interruptInactiveRuntimeTaskIfNeeded(taskRun task.TaskRun) bool {
	if taskRun.Status != task.TaskStatusRunning && taskRun.Status != task.TaskStatusPlanned {
		return false
	}
	if connectorRuntime.taskRunService.IsTaskRunActuallyRunning(taskRun) {
		return false
	}
	_, isInterrupted := connectorRuntime.taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, "runtime no longer owns this execution")
	return isInterrupted
}

func (connectorRuntime *ConnectorRuntime) hasActiveTaskForPerson(personID string) bool {
	return len(connectorRuntime.activeTaskRunsForPerson(personID)) > 0
}

func (connectorRuntime *ConnectorRuntime) taskRunWasCancelled(taskRunID string) bool {
	taskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	return isFound && taskRun.Status == task.TaskStatusCancelled
}

func (connectorRuntime *ConnectorRuntime) shouldProcessBeforeConversationLock(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) bool {
	if exactTaskControlIntent(event.Prompt) != agentcontract.TaskControlIntentNone {
		return true
	}
	return connectorRuntime.looksLikeActiveTaskFollowUp(ctx, adapter, event)
}

func (connectorRuntime *ConnectorRuntime) looksLikeActiveTaskFollowUp(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent) bool {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(adapter.Name(), event.SenderID)
	if !isFound {
		return false
	}
	activeTaskRun, isFound := connectorRuntime.latestCurrentConversationActiveTask(personID, event.ConversationID)
	if !isFound {
		return false
	}
	isRelated, errorValue := connectorRuntime.intakeClassifier.ClassifyActiveTaskFollowUp(ctx, agentcontract.ActiveTaskFollowUpClassificationRequest{
		ActiveTaskPrompt: activeTaskRun.Prompt,
		ActiveTaskStatus: string(activeTaskRun.Status),
		LatestMessage:    event.Prompt,
	})
	if errorValue != nil {
		return false
	}
	return isRelated
}

func exactTaskControlIntent(prompt string) agentcontract.TaskControlIntent {
	normalizedPrompt := strings.ToLower(strings.TrimSpace(prompt))
	switch normalizedPrompt {
	case "/stop":
		return agentcontract.TaskControlIntentStop
	case "/stop-all":
		return agentcontract.TaskControlIntentStopAll
	default:
		return agentcontract.TaskControlIntentNone
	}
}

func isTaskControlActiveStatus(status task.TaskStatus) bool {
	switch status {
	case task.TaskStatusPlanned, task.TaskStatusRunning, task.TaskStatusWaitingApproval, task.TaskStatusWaitingUserInput, task.TaskStatusBlocked:
		return true
	default:
		return false
	}
}

func taskControlReply(selection taskControlSelection, responseLanguage string) string {
	if selection.hasNoTarget {
		if responseLanguage == "en" {
			return "There is no active task to stop right now."
		}
		return "현재 중단할 작업이 없습니다."
	}
	if responseLanguage == "en" {
		return "Stopped " + taskControlCountText(len(selection.cancelledTaskRuns)) + ". Scheduled future runs were not cancelled."
	}
	return "진행 중인 작업 " + taskControlCountText(len(selection.cancelledTaskRuns)) + "을 중단했습니다. 예약된 반복 실행은 유지됩니다."
}

func taskControlCountText(count int) string {
	return fmt.Sprint(count)
}
