package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (connectorRuntime *ConnectorRuntime) reawaitPendingHolds(ctx context.Context) {
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRun() {
		if !isAwaitingAnAnswer(taskRun) {
			continue
		}
		go connectorRuntime.reawaitHold(ctx, taskRun)
	}
}

func isAwaitingAnAnswer(taskRun task.TaskRun) bool {
	return taskRun.Status == task.TaskStatusWaitingApproval || taskRun.Status == task.TaskStatusInterrupted
}

func (connectorRuntime *ConnectorRuntime) reawaitHold(ctx context.Context, taskRun task.TaskRun) {
	if awaitAnswer, isAwaiting := connectorRuntime.beginReawaiting(ctx, taskRun); isAwaiting {
		awaitAnswer()
	}
}

func (connectorRuntime *ConnectorRuntime) reawaitHeldQuestionsIn(ctx context.Context, personID string, conversationID string) {
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRunByPersonID(personID) {
		if !isAwaitingAnAnswer(taskRun) || taskRun.OriginConversationID != conversationID {
			continue
		}
		if awaitAnswer, isAwaiting := connectorRuntime.beginReawaiting(context.WithoutCancel(ctx), taskRun); isAwaiting {
			go awaitAnswer()
		}
	}
}

func (connectorRuntime *ConnectorRuntime) beginReawaiting(ctx context.Context, taskRun task.TaskRun) (func(), bool) {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	heldCall, isHeld := approvalgate.PendingHeldCall(taskEvents)
	if !isHeld || connectorRuntime.isAwaitedInThread(taskRun.TaskRunID) {
		return nil, false
	}
	turn, isReady := connectorRuntime.restartedQuestionTurn(ctx, taskRun, taskEvents)
	if !isReady {
		connectorRuntime.endUnansweredHold(ctx, taskRun, nil, approvalgate.AskUnreachable)
		return nil, false
	}
	if time.Since(taskRun.UpdatedAt) > connectorRuntime.askingThreads.expiry {
		connectorRuntime.endUnansweredHold(ctx, taskRun, turn, approvalgate.AskExpired)
		return nil, false
	}
	thread := connectorRuntime.restartedThread(taskRun, turn, heldCall.Confirmation, taskEvents)
	leave := connectorRuntime.askingThreads.join(thread)
	return func() {
		defer leave()
		connectorRuntime.awaitReawakenedThread(ctx, taskRun, taskEvents, turn, thread, heldCall)
	}, true
}

func (connectorRuntime *ConnectorRuntime) awaitReawakenedThread(ctx context.Context, taskRun task.TaskRun, taskEvents []task.TaskEvent, turn *inboundTurn, thread *askingThread, heldCall agentcontract.HeldCall) {
	if !approvalQuestionIsPosted(taskEvents) && connectorRuntime.deliverApprovalQuestion(ctx, turn, taskRun.TaskRunID, heldCall.Confirmation) != nil {
		connectorRuntime.endUnansweredHold(ctx, taskRun, turn, approvalgate.AskUnreachable)
		return
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".approval.question_awaited_after_restart", "taskRunID", taskRun.TaskRunID)
	connectorRuntime.askingThreads.markPosted(thread)
	optionID, status := connectorRuntime.awaitAnswer(ctx, thread, connectorRuntime.askingThreads.expiry-time.Since(taskRun.UpdatedAt))
	switch status {
	case approvalgate.AskAnswered:
		connectorRuntime.resumeAnsweredHold(ctx, taskRun, taskEvents, turn, optionID)
	case approvalgate.AskExpired:
		connectorRuntime.endUnansweredHold(ctx, taskRun, turn, approvalgate.AskExpired)
	}
}

func (connectorRuntime *ConnectorRuntime) restartedThread(taskRun task.TaskRun, turn *inboundTurn, confirmation string, taskEvents []task.TaskEvent) *askingThread {
	return &askingThread{
		taskRunID:         taskRun.TaskRunID,
		requesterPersonID: taskRun.RequesterPersonID,
		platform:          turn.platform,
		conversationID:    turn.event.ConversationID,
		replyTargetID:     turn.event.ReplyTargetID,
		question:          approvalQuestionFor(confirmation, approvalgate.OfferedChoices(taskEvents)),
		operatorOptions:   operatorOptionsOfApproval(approvalgate.OfferedChoices(taskEvents)),
		answers:           make(chan string, 1),
	}
}

func (connectorRuntime *ConnectorRuntime) restartedQuestionTurn(ctx context.Context, taskRun task.TaskRun, taskEvents []task.TaskEvent) (*inboundTurn, bool) {
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(taskRun, taskEvents)
	if !isFound {
		return connectorRuntime.requesterDirectMessageTurn(ctx, taskRun.RequesterPersonID)
	}
	adapter, errorValue := connectorRuntime.findAdapter(launchContext.Platform)
	if errorValue != nil {
		return nil, false
	}
	event := interruptedTaskResumeEvent(taskRun, launchContext)
	return &inboundTurn{
		adapter:     adapter,
		platform:    launchContext.Platform,
		event:       event,
		replyTarget: ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID, DedupeKey: event.DedupeKey()},
		sendReply:   connectorRuntime.recordingDelivery(adapter.SendReply),
	}, true
}

func (connectorRuntime *ConnectorRuntime) resumeAnsweredHold(ctx context.Context, taskRun task.TaskRun, taskEvents []task.TaskEvent, turn *inboundTurn, optionID string) {
	settlement, errorValue := connectorRuntime.approvalGate.SettleAnswer(ctx, connectorRuntime.restartedApprovalRequest(taskRun, turn), approvalAnswerOfOption(optionID))
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+turn.platform+".approval.answered_run_will_not_resume", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return
	}
	settledCalls := []agentcontract.CarriedOutCall{}
	if settlement.DeferredCall != nil {
		settledCalls = append(settledCalls, *settlement.DeferredCall)
	}
	launchContext := resumeLaunchContextOf(taskRun, taskEvents, turn)
	if _, errorValue := connectorRuntime.resumeTaskRun(ctx, taskRun, taskEvents, launchContext, turn.adapter, settledCalls); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+turn.platform+".approval.answered_run_failed_to_resume", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
	}
}

func resumeLaunchContextOf(taskRun task.TaskRun, taskEvents []task.TaskEvent, turn *inboundTurn) interruptedTaskLaunchContext {
	if launchContext, isFound := interruptedTaskLaunchContextFromEvents(taskRun, taskEvents); isFound {
		return launchContext
	}
	return interruptedTaskLaunchContext{
		ProfileName:      "default",
		ConversationID:   turn.event.ConversationID,
		ReplyTargetID:    turn.event.ReplyTargetID,
		ConversationType: turn.event.Context.ConversationType,
		Platform:         turn.platform,
	}
}

func (connectorRuntime *ConnectorRuntime) restartedApprovalRequest(taskRun task.TaskRun, turn *inboundTurn) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: taskRun.RequesterPersonID,
		TaskRunID:         taskRun.TaskRunID,
		Prompt:            taskRun.Prompt,
		Platform:          turn.platform,
		ConversationID:    turn.event.ConversationID,
		ReplyTargetID:     firstNonEmptyString(taskRun.OriginReplyTargetID, turn.event.ReplyTargetID),
	}
}

func (connectorRuntime *ConnectorRuntime) endUnansweredHold(ctx context.Context, taskRun task.TaskRun, turn *inboundTurn, status approvalgate.AskStatus) {
	rejection := agentcontract.ApprovalSignalReject
	approvalgate.SettleSignal(connectorRuntime.taskRunService, taskRun.TaskRunID, &rejection, string(status))
	connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, endedHoldEventName(status), marshalApprovalEnd(taskRun, status))
	turnResult := connectorRuntime.launchFailureCompleter.CompleteLaunchFailure(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID:   taskRun.RequesterPersonID,
		RequesterEmail:      connectorRuntime.identityService.ResolvePersonPrimaryEmail(taskRun.RequesterPersonID),
		ExistingTaskRunID:   taskRun.TaskRunID,
		OriginReplyTargetID: taskRun.OriginReplyTargetID,
		OriginIsThread:      taskRun.OriginIsThread,
		ConversationID:      taskRun.OriginConversationID,
		Prompt:              taskRun.Prompt,
		ResponseLanguage:    latestApprovalResponseLanguage(connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)),
	}, "approval", "approval_"+string(status), errors.New(unansweredHoldReasons[status]))
	if turn == nil {
		return
	}
	if _, errorValue := connectorRuntime.dispatchTaskReply(withConnectorEvent(ctx, turn.event), turn.platform, turn.adapter, turn.event, turn.replyTarget, turnResult, "", turn.sendReply); errorValue != nil {
		connectorRuntime.logger.Error("connector."+turn.platform+".approval.end_notice_failed", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
	}
}

var unansweredHoldReasons = map[approvalgate.AskStatus]string{
	approvalgate.AskExpired:     "nobody answered the approval question within 24 hours, so the call was not approved and did not run",
	approvalgate.AskUnreachable: "the approval question could not be put to the requester, so the call was not approved and did not run",
}

func endedHoldEventName(status approvalgate.AskStatus) string {
	if status == approvalgate.AskExpired {
		return approvalgate.TaskEventApprovalExpired
	}
	return approvalgate.TaskEventApprovalUnreachable
}

func marshalApprovalEnd(taskRun task.TaskRun, status approvalgate.AskStatus) string {
	document, _ := json.Marshal(map[string]string{"requesterPersonID": taskRun.RequesterPersonID, "reason": string(status)})
	return string(document)
}

func approvalQuestionIsPosted(taskEvents []task.TaskEvent) bool {
	isPosted := false
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHoldOpened:
			isPosted = false
		case agentcontract.TaskEventConnectorReplySent, agentcontract.TaskEventConnectorReplyEnqueued:
			isPosted = isPosted || replyKindCarriesQuestion(taskEvent.Body)
		}
	}
	return isPosted
}

func replyKindCarriesQuestion(body string) bool {
	sent := struct {
		ReplyKind string `json:"replyKind"`
	}{}
	if json.Unmarshal([]byte(body), &sent) != nil {
		return false
	}
	replyKind := strings.TrimSpace(sent.ReplyKind)
	return replyKind == connectorReplyKindApprovalQuestion || replyKind == connectorReplyKindSuccess
}
