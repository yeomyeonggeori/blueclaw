package connectors

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (connectorRuntime *ConnectorRuntime) reawaitPendingHolds(ctx context.Context) {
	for _, taskRun := range connectorRuntime.taskRunService.ListTaskRun() {
		if taskRun.Status != task.TaskStatusWaitingApproval {
			continue
		}
		if time.Since(taskRun.UpdatedAt) > approvalExpiry {
			connectorRuntime.endHoldQuietly(taskRun, approvalgate.AskExpired)
			continue
		}
		go connectorRuntime.reawaitHold(ctx, taskRun)
	}
}

func (connectorRuntime *ConnectorRuntime) reawaitHold(ctx context.Context, taskRun task.TaskRun) {
	taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	heldCall, isHeld := approvalgate.PendingHeldCall(taskEvents)
	if !isHeld || connectorRuntime.isAwaitedInThread(taskRun.TaskRunID) {
		return
	}
	turn, isReady := connectorRuntime.restartedQuestionTurn(ctx, taskRun, taskEvents)
	if !isReady {
		connectorRuntime.endHoldQuietly(taskRun, approvalgate.AskUnreachable)
		return
	}
	thread := connectorRuntime.restartedThread(taskRun, turn, heldCall.Confirmation, taskEvents)
	defer connectorRuntime.askingThreads.join(thread)()
	if !approvalQuestionIsPosted(taskEvents) && connectorRuntime.deliverApprovalQuestion(ctx, turn, taskRun.TaskRunID, heldCall.Confirmation) != nil {
		connectorRuntime.endHoldQuietly(taskRun, approvalgate.AskUnreachable)
		return
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".approval.question_awaited_after_restart", "taskRunID", taskRun.TaskRunID)
	connectorRuntime.askingThreads.markPosted(thread)
	optionID, status := connectorRuntime.awaitAnswer(ctx, thread, approvalExpiry-time.Since(taskRun.UpdatedAt))
	switch status {
	case approvalgate.AskAnswered:
		connectorRuntime.resumeAnsweredHold(ctx, taskRun, taskEvents, turn, optionID)
	case approvalgate.AskExpired:
		connectorRuntime.endExpiredHold(ctx, taskRun, turn, heldCall.Confirmation)
	}
}

func (connectorRuntime *ConnectorRuntime) restartedThread(taskRun task.TaskRun, turn *inboundTurn, confirmation string, taskEvents []task.TaskEvent) *askingThread {
	return &askingThread{
		taskRunID:         taskRun.TaskRunID,
		requesterPersonID: taskRun.RequesterPersonID,
		platform:          turn.platform,
		conversationID:    turn.event.ConversationID,
		replyTargetID:     turn.event.ReplyTargetID,
		question:          approvalQuestionFor(confirmation, approvalrecord.OfferedChoices(taskEvents)),
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

func (connectorRuntime *ConnectorRuntime) endExpiredHold(ctx context.Context, taskRun task.TaskRun, turn *inboundTurn, confirmation string) {
	connectorRuntime.endHoldQuietly(taskRun, approvalgate.AskExpired)
	reply, errorValue := connectorRuntime.replyGenerator.GenerateReply(ctx, expiredApprovalReplyPrompt(confirmation, latestApprovalResponseLanguage(connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))))
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+turn.platform+".approval.expiry_reply_failed", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return
	}
	if _, errorValue := turn.sendReply(withConnectorEvent(ctx, turn.event), turn.replyTarget, OutboundReply{Message: reply, TaskRunID: taskRun.TaskRunID}); errorValue != nil {
		connectorRuntime.logger.Error("connector."+turn.platform+".outbound.failed", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) endHoldQuietly(taskRun task.TaskRun, status approvalgate.AskStatus) {
	rejection := agentcontract.ApprovalSignalReject
	approvalrecord.SettleSignal(connectorRuntime.taskRunService, taskRun.TaskRunID, &rejection, string(status))
	connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, endedHoldEventName(status), marshalApprovalEnd(taskRun, status))
	_, _ = connectorRuntime.taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, taskRun.RequesterPersonID, "approval."+string(status))
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

func expiredApprovalReplyPrompt(confirmation string, responseLanguage string) string {
	return strings.Join([]string{
		connectorResponseLanguageInstruction(responseLanguage),
		"Nobody answered this approval question within 24 hours, so the action was not carried out. Write one brief user-facing reply saying that it did not run for that reason.",
		"The question was: " + strings.TrimSpace(confirmation),
	}, "\n")
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
