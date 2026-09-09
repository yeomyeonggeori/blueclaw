package connectors

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type ConnectorRevisionRepository interface {
	ListUnansweredConnectorEvents() ([]PlatformInboundEvent, error)
	SuppressConnectorReply(QueuedConnectorReply, string) error
}

const SupersededRequestReason = "superseded_by_new_message"
const requestAlreadyRunningReason = "request_already_running"

func taskRunMatchesMessageScope(taskRun task.TaskRun, event PlatformInboundEvent) bool {
	if event.IsThread == nil && !isMultiPersonConversation(event) {
		return taskRun.OriginConversationID == event.ConversationID
	}
	return taskRunMatchesStopScope(taskRun, event)
}

func (connectorRuntime *ConnectorRuntime) restorePendingRequests() error {
	repository, isSupported := connectorRuntime.eventRepository.(ConnectorRevisionRepository)
	if !isSupported {
		return nil
	}
	events, errorValue := repository.ListUnansweredConnectorEvents()
	if errorValue != nil {
		return errorValue
	}
	store := connectorRuntime.pendingRequests
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for _, event := range events {
		preparedEvent, previous := store.prepare(event)
		store.register(preparedEvent, previous)
	}
	return nil
}

func (connectorRuntime *ConnectorRuntime) processPendingInboundEvent(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error), isQueued bool) (ConnectorRuntimeResult, error) {
	if exactTaskControlIntent(event.Prompt) != agentcontract.TaskControlIntentNone {
		result, errorValue := connectorRuntime.processInboundEventWithReplySender(ctx, adapter, event, sendReply)
		if errorValue == nil && result.ReplyDispatchID != "" {
			connectorRuntime.closeControlledPendingRequests(event)
		}
		return result, errorValue
	}
	connectorRuntime.refreshPendingRequestDelivery(event)
	requestContext, request, startReason := connectorRuntime.pendingRequests.begin(ctx, event)
	if startReason == pendingRequestAlreadyRunning {
		return ConnectorRuntimeResult{Handled: true, Platform: event.Platform, Reason: requestAlreadyRunningReason}, nil
	}
	if startReason == pendingRequestSuperseded {
		return ConnectorRuntimeResult{Handled: true, Platform: event.Platform, Reason: SupersededRequestReason}, nil
	}
	defer connectorRuntime.finishPendingRequest(request)
	stopCancellation := context.AfterFunc(requestContext, func() {
		if connectorRuntime.pendingRequests.isSuperseded(event.DedupeKey()) {
			connectorRuntime.cancelPendingRequestTask(event)
		}
	})
	defer stopCancellation()
	for previous := request.previous; previous != nil; previous = previous.previous {
		connectorRuntime.cancelPendingRequestTask(previous.event)
		select {
		case <-previous.done:
		case <-requestContext.Done():
			return connectorRuntime.interruptedPendingRequestResult(event, requestContext)
		}
	}
	if requestContext.Err() != nil {
		return connectorRuntime.interruptedPendingRequestResult(event, requestContext)
	}
	guardedSender := connectorRuntime.pendingRequestReplySender(event.DedupeKey(), sendReply, !isQueued)
	result, errorValue := connectorRuntime.processInboundEventWithReplySender(requestContext, adapter, revisedRequestEvent(request.event), guardedSender)
	if connectorRuntime.pendingRequests.isSuperseded(event.DedupeKey()) {
		return ConnectorRuntimeResult{Handled: true, Platform: event.Platform, TaskRunID: result.TaskRunID, Reason: SupersededRequestReason}, nil
	}
	return result, errorValue
}

func (connectorRuntime *ConnectorRuntime) refreshPendingRequestDelivery(event PlatformInboundEvent) {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(event.Platform, event.SenderID)
	if !isFound {
		return
	}
	store := connectorRuntime.pendingRequests
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for sourceReference, request := range store.requests {
		if request.hasReply || request.isSuperseded || !pendingRequestsShareScope(request.event, event) {
			continue
		}
		taskRun, isFound := connectorRuntime.findTaskRunBySourceReference(personID, sourceReference)
		if isFound && connectorRuntime.agentAlreadyReplied(taskRun.TaskRunID, request.event.ConversationID, request.event.ReplyTargetID) {
			request.hasReply = true
			if request.isFinished {
				delete(store.requests, sourceReference)
			}
		}
	}
}

func (connectorRuntime *ConnectorRuntime) interruptedPendingRequestResult(event PlatformInboundEvent, ctx context.Context) (ConnectorRuntimeResult, error) {
	if connectorRuntime.pendingRequests.isSuperseded(event.DedupeKey()) {
		return ConnectorRuntimeResult{Handled: true, Platform: event.Platform, Reason: SupersededRequestReason}, nil
	}
	return ConnectorRuntimeResult{}, ctx.Err()
}

func (connectorRuntime *ConnectorRuntime) closeControlledPendingRequests(event PlatformInboundEvent) {
	store := connectorRuntime.pendingRequests
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for _, request := range store.requests {
		isSameSender := request.event.Platform == event.Platform && request.event.SenderID == event.SenderID
		if isSameSender && (exactTaskControlIntent(event.Prompt) == agentcontract.TaskControlIntentStopAll || pendingRequestsShareScope(request.event, event)) {
			request.hasReply = true
		}
	}
}

func (connectorRuntime *ConnectorRuntime) suppressSupersededQueuedReply(reply QueuedConnectorReply) bool {
	if !connectorRuntime.pendingRequests.isSuperseded(reply.RawEventID) {
		return false
	}
	var errorValue error
	if repository, isSupported := connectorRuntime.eventRepository.(ConnectorRevisionRepository); isSupported {
		errorValue = repository.SuppressConnectorReply(reply, SupersededRequestReason)
	} else {
		errorValue = connectorRuntime.outboxRepository().MarkConnectorReplyFailed(reply, errors.New(SupersededRequestReason), time.Time{})
	}
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.reply.suppress_failed", slog.String("outboxID", reply.OutboxID), slog.String("error", errorValue.Error()))
	}
	connectorRuntime.appendConnectorReplyEvent(reply.Reply.TaskRunID, "connector.reply.suppressed", map[string]string{"outboxID": reply.OutboxID, "reason": SupersededRequestReason})
	return true
}

func (connectorRuntime *ConnectorRuntime) finishPendingRequest(request *pendingRequest) {
	if connectorRuntime.pendingRequests.isSuperseded(request.event.DedupeKey()) {
		connectorRuntime.cancelPendingRequestTask(request.event)
	}
	connectorRuntime.pendingRequests.finish(request)
}

func (connectorRuntime *ConnectorRuntime) cancelPendingRequestTask(event PlatformInboundEvent) {
	personID, isFound := connectorRuntime.identityService.ResolvePersonIDByPlatformAccount(event.Platform, event.SenderID)
	if !isFound {
		return
	}
	connectorRuntime.cancelPendingSourceTask(personID, event.Platform, event.ConversationID, event.DedupeKey())
}

func (connectorRuntime *ConnectorRuntime) cancelPendingSourceTask(personID string, platform string, conversationID string, sourceReference string) {
	taskRun, isFound := connectorRuntime.findTaskRunBySourceReference(personID, sourceReference)
	if !isFound || !isTaskControlActiveStatus(taskRun.Status) {
		return
	}
	if _, errorValue := connectorRuntime.taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, personID, SupersededRequestReason); errorValue != nil {
		connectorRuntime.logger.Warn("connector.request.cancel_failed", slog.String("taskRunID", taskRun.TaskRunID), slog.String("error", errorValue.Error()))
		return
	}
	connectorRuntime.resolveOpenTaskWaitsForTaskRun(personID, platform, conversationID, taskRun.TaskRunID)
	connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "task.superseded_by_message", marshalConnectorEventBody(map[string]string{"sourceReference": sourceReference}))
}

func (connectorRuntime *ConnectorRuntime) pendingRequestReplySender(sourceReference string, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error), isDelivery bool) func(context.Context, ReplyTarget, OutboundReply) (string, error) {
	return func(ctx context.Context, target ReplyTarget, reply OutboundReply) (string, error) {
		store := connectorRuntime.pendingRequests
		store.mutex.Lock()
		defer store.mutex.Unlock()
		request := store.requests[sourceReference]
		if store.supersededReferences[sourceReference] {
			return "", nil
		}
		dispatchID, errorValue := sendReply(ctx, target, reply)
		if request != nil && isDelivery && errorValue == nil && dispatchID != "" {
			request.hasReply = true
			if request.isFinished {
				delete(store.requests, sourceReference)
			}
		}
		return dispatchID, errorValue
	}
}

func (connectorRuntime *ConnectorRuntime) revisedPriorTask(personID string, event PlatformInboundEvent) (agentcontract.PriorTaskContext, bool) {
	priorTask := agentcontract.PriorTaskContext{}
	evidence := []revisedRequestEvidence{}
	for _, message := range event.PreviousMessages {
		taskRun, isFound := connectorRuntime.findTaskRunBySourceReference(personID, message.SourceReference)
		if !isFound {
			continue
		}
		taskEvents := connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
		priorTask = agentcontract.PriorTaskContext{
			TaskRunID:     taskRun.TaskRunID,
			Status:        string(taskRun.Status),
			Prompt:        event.Prompt,
			FailureReason: taskRun.FailureReason,
		}
		evidence = append(evidence, revisedTaskEvidence(taskRun, taskEvents))
	}
	if len(evidence) == 0 {
		return priorTask, false
	}
	priorTask.Result = "Recorded work from the superseded requests. Cancellation does not undo these effects. Inspect and amend existing results to satisfy the revised request without duplicating them.\n" + marshalConnectorEventBody(evidence)
	return priorTask, true
}

type revisedRequestEvidence struct {
	TaskRunID   string           `json:"taskRunID"`
	Status      task.TaskStatus  `json:"status"`
	Prompt      string           `json:"prompt"`
	Result      string           `json:"result,omitempty"`
	ToolResults []task.TaskEvent `json:"toolResults,omitempty"`
}

func revisedTaskEvidence(taskRun task.TaskRun, events []task.TaskEvent) revisedRequestEvidence {
	evidence := revisedRequestEvidence{TaskRunID: taskRun.TaskRunID, Status: taskRun.Status, Prompt: taskRun.Prompt, Result: taskRun.Result}
	for _, event := range events {
		if strings.HasPrefix(event.Name, "tool.") && strings.HasSuffix(event.Name, ".result") {
			evidence.ToolResults = append(evidence.ToolResults, event)
		}
	}
	return evidence
}
