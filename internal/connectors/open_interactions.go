package connectors

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type openInteractions struct {
	ask            AskInteraction
	askTaskRun     task.TaskRun
	hasAsk         bool
	runningTask    task.TaskRun
	hasRunningTask bool
}

func (open openInteractions) isEmpty() bool {
	return !open.hasAsk && !open.hasRunningTask
}

func (connectorRuntime *ConnectorRuntime) readOpenInteractions(turn *inboundTurn) openInteractions {
	open := openInteractions{}
	open.ask, open.hasAsk = connectorRuntime.findPendingAskInteraction(turn.personID, turn.event)
	if open.hasAsk {
		open.askTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(open.ask.TaskRunID)
	}
	open.runningTask, open.hasRunningTask = connectorRuntime.latestRunningConversationTask(turn.personID, turn.event)
	return open
}

const askReplyReason = "a reply in the thread the run asked its question in"

func (connectorRuntime *ConnectorRuntime) recordAskReplyClassified(turn *inboundTurn, ask AskInteraction) {
	connectorRuntime.taskRunService.AppendTaskEvent(ask.TaskRunID, agentcontract.TaskEventAskReplyClassified, agentruntime.MarshalBody(map[string]any{
		"messageID": turn.event.MessageID,
		"choices":   []string{},
		"route":     agentcontract.TurnRouteContinueTask,
		"reason":    askReplyReason,
	}))
}

func (connectorRuntime *ConnectorRuntime) recordBusyRoute(turn *inboundTurn, runningTask task.TaskRun, busyRoute inboundengagement.BusyRoute) {
	connectorRuntime.taskRunService.AppendTaskEvent(runningTask.TaskRunID, agentcontract.TaskEventTaskBusyMessageRouted, agentruntime.MarshalBody(map[string]string{
		"messageID":       turn.event.MessageID,
		"busyRoute":       string(busyRoute),
		"latestUserInput": strings.TrimSpace(turn.event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) recordConfirmationReplyClassified(taskRunID string, event PlatformInboundEvent, decision answeredOption) {
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConfirmationReplyClassified, agentruntime.MarshalBody(map[string]any{
		"messageID":   event.MessageID,
		"route":       agentcontract.TurnRouteContinueTask,
		"approval":    decision.Approval,
		"choices":     decision.Choices,
		"reason":      askReplyReason,
		"replyPrompt": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) settleOpenInteractions(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	open := connectorRuntime.readOpenInteractions(turn)
	if open.isEmpty() {
		return connectorRuntime.settleFinishedTaskFollowUp(ctx, turn)
	}
	if !open.hasAsk && isIgnoredWithoutDeciding(turn.event) {
		return ConnectorRuntimeResult{}, false, nil
	}
	if open.hasAsk {
		connectorRuntime.settleAsk(turn, open.ask)
	}
	if open.hasRunningTask && !turn.hasPendingAskInteraction && len(turn.event.PreviousMessages) == 0 {
		return connectorRuntime.settleRunningTask(ctx, turn, open.runningTask)
	}
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) settleAsk(turn *inboundTurn, ask AskInteraction) {
	connectorRuntime.recordAskReplyClassified(turn, ask)
	connectorRuntime.appendAskResolvedEvent(ask, turn.event)
	turn.pendingAskInteraction = ask
	turn.hasPendingAskInteraction = true
}

func (connectorRuntime *ConnectorRuntime) settleRunningTask(ctx context.Context, turn *inboundTurn, runningTask task.TaskRun) (ConnectorRuntimeResult, bool, error) {
	judgment, errorValue := connectorRuntime.judgeInboundMessage(ctx, turn.adapter, turn.event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	connectorRuntime.recordBusyRoute(turn, runningTask, judgment.BusyRoute)
	busyResult, errorValue := connectorRuntime.settleBusyDecision(ctx, turn.platform, turn.event, turn.replyTarget, runningTask, judgment.BusyRoute, turn.sendReply)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	if busyResult.isHandled {
		return busyResult.connectorResult, true, nil
	}
	turn.clearsActiveGoal = busyResult.clearActiveGoal
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) settleFinishedTaskFollowUp(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	if len(turn.event.PreviousMessages) > 0 || isIgnoredWithoutDeciding(turn.event) {
		return ConnectorRuntimeResult{}, false, nil
	}
	busyResult, errorValue := connectorRuntime.handlePossibleFinishedTaskFollowUp(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, turn.personID, turn.sendReply)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	if busyResult.isHandled {
		return busyResult.connectorResult, true, nil
	}
	return ConnectorRuntimeResult{}, false, nil
}
