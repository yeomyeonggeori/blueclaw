package connectors

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type openInteractions struct {
	ask            AskInteraction
	askAt          time.Time
	hasAsk         bool
	runningTask    task.TaskRun
	hasRunningTask bool
}

func (open openInteractions) isEmpty() bool {
	return !open.hasAsk && !open.hasRunningTask
}

func (open openInteractions) ledgerTaskRunID() string {
	switch {
	case open.hasAsk:
		return open.ask.TaskRunID
	default:
		return open.runningTask.TaskRunID
	}
}

func (connectorRuntime *ConnectorRuntime) readOpenInteractions(turn *inboundTurn) openInteractions {
	open := openInteractions{}
	open.ask, open.hasAsk = connectorRuntime.findPendingAskInteraction(turn.personID, turn.platform, turn.event, turn.taskWaitResolution)
	if open.hasAsk {
		askTaskRun, _ := connectorRuntime.taskRunService.FindTaskRun(open.ask.TaskRunID)
		open.askAt = latestAskRequestedTime(connectorRuntime.taskRunService.ListTaskEvent(open.ask.TaskRunID), askTaskRun.UpdatedAt)
	}
	open.runningTask, open.hasRunningTask = connectorRuntime.latestRunningConversationTask(turn.personID, turn.event)
	return open
}

func latestAskRequestedTime(taskEvents []task.TaskEvent, fallback time.Time) time.Time {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if task.IsAskRequestedEvent(taskEvents[index].Name) {
			return taskEvents[index].CreatedAt
		}
	}
	return fallback
}

func (connectorRuntime *ConnectorRuntime) exchangesSince(turn *inboundTurn, askedAt time.Time, askingTaskRunID string) int {
	count := 0
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(turn.personID) {
		if !taskRunSharesMessageThread(taskRun, turn.event) || taskRun.TaskRunID == askingTaskRunID {
			continue
		}
		if taskRun.CreatedAt.After(askedAt) {
			count++
		}
	}
	return count
}

func (connectorRuntime *ConnectorRuntime) routeOpenInteractions(ctx context.Context, turn *inboundTurn, open openInteractions) (agentcontract.TurnDecision, error) {
	request := agentcontract.AgentRequest{
		RequesterPersonID: turn.personID,
		ConversationID:    turn.event.ConversationID,
		Prompt:            turn.event.Prompt,
		ResponseLanguage:  responseLanguageForEvent(turn.event),
		VisibleContext:    turn.event.Context.ToAgentVisibleContext(),
		ToolSet:           turn.routerToolSet,
	}
	if !open.hasAsk {
		request.DecidedTurnFields = connectorRuntime.decidedTurnFields(ctx, turn.adapter, turn.event)
	}
	if open.hasRunningTask {
		request.ActiveTask = connectorRuntime.activeTaskContext(open.runningTask)
	}
	decision, errorValue := connectorRuntime.decideOpenInteractions(ctx, turn, open, request)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	connectorRuntime.recordOpenInteractionRouting(turn, open, decision)
	return decision, nil
}

func (connectorRuntime *ConnectorRuntime) decideOpenInteractions(ctx context.Context, turn *inboundTurn, open openInteractions, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	if open.hasAsk {
		return answeringTheQuestionTheRunAsked(turn.event), nil
	}
	return connectorRuntime.planTurn(ctx, open.ledgerTaskRunID(), request)
}

func answeringTheQuestionTheRunAsked(event PlatformInboundEvent) agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteContinueTask,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeMaintenanceTask,
		ResponseLanguage: responseLanguageForEvent(event),
		Reason:           "a reply in the thread the run asked its question in",
	}
}

func (connectorRuntime *ConnectorRuntime) recordOpenInteractionRouting(turn *inboundTurn, open openInteractions, decision agentcontract.TurnDecision) {
	if open.hasAsk {
		connectorRuntime.taskRunService.AppendTaskEvent(open.ask.TaskRunID, agentcontract.TaskEventAskReplyClassified, agentruntime.MarshalBody(map[string]any{
			"messageID": turn.event.MessageID,
			"choices":   decision.Choices,
			"route":     decision.Route,
			"reason":    decision.Reason,
		}))
	}
	if open.hasRunningTask {
		connectorRuntime.taskRunService.AppendTaskEvent(open.runningTask.TaskRunID, agentcontract.TaskEventTaskBusyMessageRouted, agentruntime.MarshalBody(map[string]string{
			"messageID":       turn.event.MessageID,
			"busyRoute":       string(decision.BusyRoute),
			"reason":          strings.TrimSpace(decision.Reason),
			"latestUserInput": strings.TrimSpace(turn.event.Prompt),
		}))
	}
}

func (connectorRuntime *ConnectorRuntime) recordConfirmationReplyClassified(taskRunID string, event PlatformInboundEvent, decision agentcontract.TurnDecision) {
	connectorRuntime.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventConfirmationReplyClassified, agentruntime.MarshalBody(map[string]any{
		"messageID":   event.MessageID,
		"route":       decision.Route,
		"approval":    decision.Approval,
		"choices":     decision.Choices,
		"reason":      decision.Reason,
		"replyPrompt": strings.TrimSpace(event.Prompt),
	}))
}

func (connectorRuntime *ConnectorRuntime) settleOpenInteractions(ctx context.Context, turn *inboundTurn) (ConnectorRuntimeResult, bool, error) {
	open := connectorRuntime.readOpenInteractions(turn)
	turn.routerToolSet = connectorRuntime.routerToolSetForTurn(turn)
	if open.isEmpty() {
		return connectorRuntime.settleFinishedTaskFollowUp(ctx, turn)
	}
	decision, errorValue := connectorRuntime.routeOpenInteractions(ctx, turn, open)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	turn.turnDecision = decision
	turn.hasTurnDecision = true
	if open.hasAsk {
		connectorRuntime.settleAsk(turn, open.ask, decision)
	}
	if open.hasRunningTask && !turn.hasPendingAskInteraction && len(turn.event.PreviousMessages) == 0 {
		return connectorRuntime.settleRunningTask(ctx, turn, open.runningTask, decision)
	}
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) settleAsk(turn *inboundTurn, ask AskInteraction, decision agentcontract.TurnDecision) {
	if !askIsAnswered(decision) {
		connectorRuntime.logger.Info("connector."+turn.platform+".ask.kept", slog.String("messageID", turn.event.MessageID), slog.String("taskRunID", ask.TaskRunID), slog.String("route", string(decision.Route)))
		turn.keptTaskRunIDs = append(turn.keptTaskRunIDs, ask.TaskRunID)
		return
	}
	connectorRuntime.appendAskResolvedEvent(ask, turn.event, decision)
	connectorRuntime.resolveTaskWaitToken(turn.taskWaitResolution)
	turn.pendingAskInteraction = ask
	turn.hasPendingAskInteraction = true
}

func askIsAnswered(decision agentcontract.TurnDecision) bool {
	if len(decision.Choices) > 0 {
		return true
	}
	return decision.Route == agentcontract.TurnRouteContinueTask || decision.Route == agentcontract.TurnRouteReviseTask
}

func (connectorRuntime *ConnectorRuntime) settleRunningTask(ctx context.Context, turn *inboundTurn, runningTask task.TaskRun, decision agentcontract.TurnDecision) (ConnectorRuntimeResult, bool, error) {
	busyResult, errorValue := connectorRuntime.settleBusyDecision(ctx, turn.platform, turn.event, turn.replyTarget, runningTask, decision, turn.sendReply)
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
	if len(turn.event.PreviousMessages) > 0 {
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
