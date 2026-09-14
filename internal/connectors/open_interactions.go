package connectors

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type openInteractions struct {
	confirmation    pendingApproval
	confirmationAt  time.Time
	hasConfirmation bool
	ask             AskInteraction
	askAt           time.Time
	hasAsk          bool
	runningTask     task.TaskRun
	hasRunningTask  bool
}

func (open openInteractions) isEmpty() bool {
	return !open.hasConfirmation && !open.hasAsk && !open.hasRunningTask
}

func (open openInteractions) ledgerTaskRunID() string {
	switch {
	case open.hasConfirmation:
		return open.confirmation.TaskRun.TaskRunID
	case open.hasAsk:
		return open.ask.TaskRunID
	default:
		return open.runningTask.TaskRunID
	}
}

func (connectorRuntime *ConnectorRuntime) readOpenInteractions(turn *inboundTurn) openInteractions {
	open := openInteractions{}
	open.confirmation, open.hasConfirmation = connectorRuntime.findPendingApproval(turn.personID, turn.platform, turn.event, turn.taskWaitResolution)
	if open.hasConfirmation {
		open.confirmationAt = latestTaskEventTime(connectorRuntime.taskRunService.ListTaskEvent(open.confirmation.TaskRun.TaskRunID), agentcontract.TaskEventConfirmationRequested, open.confirmation.TaskRun.UpdatedAt)
	}
	open.ask, open.hasAsk = connectorRuntime.findPendingAskInteraction(turn.personID, turn.platform, turn.event, turn.taskWaitResolution)
	if open.hasAsk {
		askTaskRun, _ := connectorRuntime.taskRunService.FindTaskRun(open.ask.TaskRunID)
		open.askAt = latestTaskEventTime(connectorRuntime.taskRunService.ListTaskEvent(open.ask.TaskRunID), agentcontract.TaskEventAskRequested, askTaskRun.UpdatedAt)
	}
	open.runningTask, open.hasRunningTask = connectorRuntime.latestRunningConversationTask(turn.personID, turn.event)
	return open
}

func latestTaskEventTime(taskEvents []task.TaskEvent, name string, fallback time.Time) time.Time {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name == name {
			return taskEvents[index].CreatedAt
		}
	}
	return fallback
}

func (connectorRuntime *ConnectorRuntime) exchangesSince(turn *inboundTurn, askedAt time.Time, askingTaskRunID string) int {
	count := 0
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(turn.personID) {
		if taskRun.OriginConversationID != turn.event.ConversationID || taskRun.TaskRunID == askingTaskRunID {
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
	if open.hasConfirmation {
		request.PendingConfirmation = agentcontract.PendingConfirmationContext{
			TaskRunID:      open.confirmation.TaskRun.TaskRunID,
			Prompt:         open.confirmation.IntentPrompt,
			Question:       open.confirmation.ApprovalQuestion,
			AskedAt:        open.confirmationAt,
			ExchangesSince: connectorRuntime.exchangesSince(turn, open.confirmationAt, open.confirmation.TaskRun.TaskRunID),
		}
	}
	if open.hasAsk {
		exchanges := connectorRuntime.exchangesSince(turn, open.askAt, open.ask.TaskRunID)
		if open.ask.Kind == "ask_input" {
			request.PendingInput = agentcontract.PendingInputContext{TaskRunID: open.ask.TaskRunID, Question: open.ask.Question, SelectionMode: open.ask.SelectionMode, Options: choiceReplyOptions(open.ask.Options), AskedAt: open.askAt, ExchangesSince: exchanges}
		} else {
			request.PendingChoice = agentcontract.PendingChoiceContext{TaskRunID: open.ask.TaskRunID, Question: open.ask.Question, SelectionMode: open.ask.SelectionMode, Options: choiceReplyOptions(open.ask.Options), AskedAt: open.askAt, ExchangesSince: exchanges}
		}
	}
	if open.hasRunningTask {
		request.ActiveTask = connectorRuntime.activeTaskContext(open.runningTask)
	}
	decision, errorValue := connectorRuntime.planTurn(ctx, open.ledgerTaskRunID(), request)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	connectorRuntime.recordOpenInteractionRouting(turn, open, decision)
	return decision, nil
}

func (connectorRuntime *ConnectorRuntime) recordOpenInteractionRouting(turn *inboundTurn, open openInteractions, decision agentcontract.TurnDecision) {
	if open.hasConfirmation {
		connectorRuntime.taskRunService.AppendTaskEvent(open.confirmation.TaskRun.TaskRunID, agentcontract.TaskEventConfirmationReplyClassified, marshalConnectorEventBody(map[string]any{
			"messageID":   turn.event.MessageID,
			"route":       decision.Route,
			"approval":    decision.Approval,
			"reason":      decision.Reason,
			"replyPrompt": strings.TrimSpace(turn.event.Prompt),
		}))
	}
	if open.hasAsk {
		connectorRuntime.taskRunService.AppendTaskEvent(open.ask.TaskRunID, agentcontract.TaskEventAskReplyClassified, marshalConnectorEventBody(map[string]any{
			"messageID": turn.event.MessageID,
			"choices":   decision.Choices,
			"route":     decision.Route,
			"reason":    decision.Reason,
		}))
	}
	if open.hasRunningTask {
		connectorRuntime.taskRunService.AppendTaskEvent(open.runningTask.TaskRunID, agentcontract.TaskEventTaskBusyMessageRouted, marshalConnectorEventBody(map[string]string{
			"messageID":       turn.event.MessageID,
			"busyRoute":       string(decision.BusyRoute),
			"reason":          strings.TrimSpace(decision.Reason),
			"latestUserInput": strings.TrimSpace(turn.event.Prompt),
		}))
	}
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
	if open.hasConfirmation {
		if result, isHandled, errorValue := connectorRuntime.settleConfirmation(ctx, turn, open.confirmation, decision); isHandled {
			return result, true, errorValue
		}
	}
	if open.hasAsk {
		connectorRuntime.settleAsk(turn, open.ask, decision)
	}
	if open.hasRunningTask && !turn.isApprovalContinuation && !turn.hasPendingAskInteraction && len(turn.event.PreviousMessages) == 0 {
		return connectorRuntime.settleRunningTask(ctx, turn, open.runningTask, decision)
	}
	return ConnectorRuntimeResult{}, false, nil
}

func (connectorRuntime *ConnectorRuntime) settleConfirmation(ctx context.Context, turn *inboundTurn, confirmation pendingApproval, decision agentcontract.TurnDecision) (ConnectorRuntimeResult, bool, error) {
	approval := approvalSignalSurvivingRoute(decision.Approval, decision.Route)
	approvalgate.RecordRequesterDecision(connectorRuntime.taskRunService, confirmation.TaskRun.TaskRunID, approval, "chat_reply")
	turn.pendingApproval = confirmation
	if approval != nil && agentcontract.IsApprovingSignal(*approval) {
		if *approval == agentcontract.ApprovalSignalApproveTask {
			connectorRuntime.grantApprovalScopeForTask(confirmation.TaskRun.TaskRunID)
		}
		connectorRuntime.logger.Info("connector."+turn.platform+".confirmation.accepted", slog.String("messageID", turn.event.MessageID), slog.String("taskRunID", confirmation.TaskRun.TaskRunID))
		connectorRuntime.resolveTaskWaitToken(turn.taskWaitResolution)
		turn.isApprovalContinuation = true
		return ConnectorRuntimeResult{}, false, nil
	}
	if approval != nil && *approval == agentcontract.ApprovalSignalReject {
		connectorRuntime.resolveTaskWaitToken(turn.taskWaitResolution)
		rejection := agentcontract.ConfirmationReplyDecision{Decision: string(agentcontract.ApprovalSignalReject), Reason: decision.Reason}
		result, errorValue := connectorRuntime.handleRejectedConfirmation(ctx, turn.platform, turn.adapter, turn.event, turn.replyTarget, confirmation, rejection, turn.sendReply)
		return result, true, errorValue
	}
	if decision.Route == agentcontract.TurnRouteReviseTask {
		connectorRuntime.resolveTaskWaitToken(turn.taskWaitResolution)
		connectorRuntime.cancelPendingConfirmation(turn.event, confirmation, decision)
		return ConnectorRuntimeResult{}, false, nil
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".confirmation.kept", slog.String("messageID", turn.event.MessageID), slog.String("taskRunID", confirmation.TaskRun.TaskRunID), slog.String("route", string(decision.Route)))
	turn.keptTaskRunIDs = append(turn.keptTaskRunIDs, confirmation.TaskRun.TaskRunID)
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
	busyResult, errorValue := connectorRuntime.handlePossibleFinishedTaskFollowUp(ctx, turn.platform, turn.event, turn.replyTarget, turn.personID, turn.sendReply)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, true, errorValue
	}
	if busyResult.isHandled {
		return busyResult.connectorResult, true, nil
	}
	return ConnectorRuntimeResult{}, false, nil
}
