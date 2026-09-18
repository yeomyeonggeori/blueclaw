package connectors

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (connectorRuntime *ConnectorRuntime) enqueueInboundEvent(event PlatformInboundEvent, queueRepository ConnectorQueueRepository) (ConnectorRuntimeResult, error) {
	connectorRuntime.refreshPendingRequestDelivery(event)
	store := connectorRuntime.pendingRequests
	store.mutex.Lock()
	defer store.mutex.Unlock()
	event, previous := store.prepare(event)
	isDuplicate, result, errorValue := queueRepository.TryEnqueueConnectorEvent(event)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	if isDuplicate {
		result.Handled = true
		result.Platform = event.Platform
		result.Duplicate = true
		connectorRuntime.logger.Info("connector."+event.Platform+".event.suppressed", slog.String("source", event.Source), slog.String("reason", "duplicate"), slog.String("messageID", event.MessageID))
		return result, nil
	}
	store.register(event, previous)
	return ConnectorRuntimeResult{Handled: true, Platform: event.Platform, Reason: "queued"}, nil
}

func (connectorRuntime *ConnectorRuntime) runConnectorInboxWorker(ctx context.Context, workerIndex int) {
	workerContext, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go connectorRuntime.recordConnectorWorkerHeartbeatUntilStopped(workerContext, "inbox", workerIndex)
	for ctx.Err() == nil {
		connectorRuntime.recordConnectorWorkerHeartbeat("inbox", workerIndex)
		if connectorRuntime.processNextQueuedConnectorEvent(ctx) {
			continue
		}
		sleepConnectorWorker(ctx)
	}
}

func (connectorRuntime *ConnectorRuntime) processNextQueuedConnectorEvent(ctx context.Context) bool {
	queueRepository := connectorRuntime.queueRepository()
	if queueRepository == nil {
		return false
	}
	queuedEvents, errorValue := queueRepository.ClaimPendingConnectorEvents(connectorDecisionBurstSize, connectorClaimLeaseDuration)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.inbox.claim_failed", slog.String("error", errorValue.Error()))
		return false
	}
	if len(queuedEvents) == 0 {
		return false
	}
	connectorRuntime.decideClaimedBurst(ctx, queuedEvents)
	for _, queuedEvent := range queuedEvents {
		connectorRuntime.processQueuedConnectorEvent(ctx, queuedEvent)
	}
	return true
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorEvent(ctx context.Context, queuedEvent QueuedConnectorEvent) {
	event := queuedEvent.Event
	adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorEventFailed(queuedEvent, errorValue)
		return
	}
	event = withInboundDecision(event)
	queuedEvent.Event = event
	connectorRuntime.logConnectorQueueWait(event)
	lock := connectorRuntime.conversationLock(event.Platform + ":" + event.ConversationID)
	if event.TaskRetry == nil && (connectorRuntime.pendingRequests.isSuperseded(event.DedupeKey()) || (len(event.PreviousMessages) == 0 && connectorRuntime.shouldProcessBeforeConversationLock(ctx, adapter, event))) {
		connectorRuntime.processQueuedConnectorEventWithAdapter(ctx, adapter, queuedEvent)
		return
	}
	lockStartedAt := time.Now()
	lock.Lock()
	connectorRuntime.logConnectorLockWait(event, time.Since(lockStartedAt))
	defer lock.Unlock()
	connectorRuntime.processQueuedConnectorEventWithAdapter(ctx, adapter, queuedEvent)
}

func (connectorRuntime *ConnectorRuntime) logConnectorQueueWait(event PlatformInboundEvent) {
	if event.RawReceivedAt.IsZero() {
		return
	}
	waitDuration := time.Since(event.RawReceivedAt)
	if waitDuration < 0 {
		return
	}
	connectorRuntime.logger.Info(
		"blueclaw.connector.queue_wait",
		slog.String("platform", event.Platform),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.Int64("duration_ms", waitDuration.Milliseconds()),
	)
}

func (connectorRuntime *ConnectorRuntime) logConnectorLockWait(event PlatformInboundEvent, waitDuration time.Duration) {
	connectorRuntime.logger.Info(
		"blueclaw.connector.lock_wait",
		slog.String("platform", event.Platform),
		slog.String("messageID", event.MessageID),
		slog.String("conversationID", event.ConversationID),
		slog.Int64("duration_ms", waitDuration.Milliseconds()),
	)
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorEventWithAdapter(ctx context.Context, adapter PlatformAdapter, queuedEvent QueuedConnectorEvent) {
	event := queuedEvent.Event
	if ctx.Err() != nil {
		return
	}
	result, errorValue := connectorRuntime.processPendingInboundEvent(ctx, adapter, event, connectorRuntime.enqueueConnectorReply, true)
	if ctx.Err() != nil {
		return
	}
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorEventFailed(queuedEvent, errorValue)
		return
	}
	if shouldDeferQueuedConnectorEvent(result) {
		connectorRuntime.logger.Info("connector."+event.Platform+".inbox.deferred", slog.String("messageID", event.MessageID), slog.String("reason", result.Reason))
		return
	}
	if errorValue := connectorRuntime.queueRepository().MarkConnectorEventSucceeded(event, result); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+event.Platform+".inbox.mark_succeeded_failed", slog.String("messageID", event.MessageID), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) enqueueConnectorReply(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	event, isFound := connectorEventFromContext(ctx)
	if !isFound {
		return "", errors.New("connector event context is missing")
	}
	outboxRepository := connectorRuntime.outboxRepository()
	if outboxRepository == nil {
		if connectorRuntime.eventRepository != nil {
			return "", errors.New("connector outbox repository is required when connector event repository is configured")
		}
		adapter, errorValue := connectorRuntime.findAdapter(event.Platform)
		if errorValue != nil {
			return "", errorValue
		}
		dispatchID, errorValue := adapter.SendReply(ctx, replyTarget, reply)
		if errorValue == nil {
			connectorRuntime.sentAttachmentSources.RecordReply(event.Platform, dispatchID, reply.Attachments)
		}
		return dispatchID, errorValue
	}
	outboxID, errorValue := outboxRepository.EnqueueConnectorReply(event, replyTarget, reply)
	if errorValue == nil {
		connectorRuntime.appendConnectorReplyEvent(reply.TaskRunID, agentcontract.TaskEventConnectorReplyEnqueued, connectorReplyEventBody(event, reply, outboxID, "", ""))
	}
	return outboxID, errorValue
}

func (connectorRuntime *ConnectorRuntime) runConnectorOutboxWorker(ctx context.Context, workerIndex int) {
	workerContext, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go connectorRuntime.recordConnectorWorkerHeartbeatUntilStopped(workerContext, "outbox", workerIndex)
	for ctx.Err() == nil {
		connectorRuntime.recordConnectorWorkerHeartbeat("outbox", workerIndex)
		if connectorRuntime.processNextQueuedConnectorReply(ctx) {
			continue
		}
		sleepConnectorWorker(ctx)
	}
}

func (connectorRuntime *ConnectorRuntime) processNextQueuedConnectorReply(ctx context.Context) bool {
	outboxRepository := connectorRuntime.outboxRepository()
	if outboxRepository == nil {
		return false
	}
	queuedReplies, errorValue := outboxRepository.ClaimPendingConnectorReplies(1, connectorClaimLeaseDuration)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector.outbox.claim_failed", slog.String("error", errorValue.Error()))
		return false
	}
	if len(queuedReplies) == 0 {
		return false
	}
	connectorRuntime.processQueuedConnectorReply(ctx, queuedReplies[0])
	return true
}

func (connectorRuntime *ConnectorRuntime) processQueuedConnectorReply(ctx context.Context, queuedReply QueuedConnectorReply) {
	if connectorRuntime.suppressSupersededQueuedReply(queuedReply) {
		return
	}
	adapter, errorValue := connectorRuntime.findAdapter(queuedReply.Platform)
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorReplyFailed(queuedReply, errorValue)
		return
	}
	queuedReply.Reply.RawEventID = firstNonEmptyString(queuedReply.Reply.RawEventID, queuedReply.RawEventID)
	queuedReply.Reply.OutboxID = firstNonEmptyString(queuedReply.Reply.OutboxID, queuedReply.OutboxID)
	sendReply := connectorRuntime.pendingRequestReplySender(queuedReply.RawEventID, adapter.SendReply, true)
	dispatchID, errorValue := sendReply(ctx, queuedReply.ReplyTarget, queuedReply.Reply)
	if dispatchID == "" && errorValue == nil && connectorRuntime.suppressSupersededQueuedReply(queuedReply) {
		return
	}
	if ctx.Err() != nil {
		return
	}
	if errorValue != nil {
		connectorRuntime.markQueuedConnectorReplyFailed(queuedReply, errorValue)
		return
	}
	connectorRuntime.sentAttachmentSources.RecordReply(queuedReply.Platform, dispatchID, queuedReply.Reply.Attachments)
	if errorValue := connectorRuntime.outboxRepository().MarkConnectorReplySent(queuedReply, dispatchID); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+queuedReply.Platform+".outbox.mark_sent_failed", slog.String("outboxID", queuedReply.OutboxID), slog.String("error", errorValue.Error()))
	}
	connectorRuntime.appendConnectorReplyEvent(queuedReply.Reply.TaskRunID, agentcontract.TaskEventConnectorReplySent, connectorReplyEventBody(PlatformInboundEvent{MessageID: queuedReply.RawEventID}, queuedReply.Reply, queuedReply.OutboxID, dispatchID, ""))
	connectorRuntime.recordTaskWaitTokenForReply(
		queuedReply.Platform,
		PlatformInboundEvent{Platform: queuedReply.Platform, ConversationID: queuedReply.ReplyTarget.ConversationID, MessageID: queuedReply.RawEventID},
		queuedReply.ReplyTarget,
		queuedReply.Reply,
		dispatchID,
	)
}

func shouldDeferQueuedConnectorEvent(result ConnectorRuntimeResult) bool {
	return result.Reason == requestAlreadyRunningReason || (result.Ignored && result.Reason == "task_intake_quiesced")
}

func (connectorRuntime *ConnectorRuntime) queueRepository() ConnectorQueueRepository {
	queueRepository, isFound := connectorRuntime.eventRepository.(ConnectorQueueRepository)
	if !isFound {
		return nil
	}
	return queueRepository
}

func (connectorRuntime *ConnectorRuntime) outboxRepository() ConnectorOutboxRepository {
	outboxRepository, isFound := connectorRuntime.eventRepository.(ConnectorOutboxRepository)
	if !isFound {
		return nil
	}
	return outboxRepository
}

func (connectorRuntime *ConnectorRuntime) markQueuedConnectorEventFailed(queuedEvent QueuedConnectorEvent, errorValue error) {
	nextAttemptAt := nextConnectorAttemptAt(queuedEvent.AttemptCount)
	if markError := connectorRuntime.queueRepository().MarkConnectorEventFailed(queuedEvent, errorValue, nextAttemptAt); markError != nil {
		connectorRuntime.logger.Warn("connector."+queuedEvent.Event.Platform+".inbox.mark_failed_failed", slog.String("messageID", queuedEvent.Event.MessageID), slog.String("error", markError.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) markQueuedConnectorReplyFailed(queuedReply QueuedConnectorReply, errorValue error) {
	connectorRuntime.appendConnectorReplyEvent(queuedReply.Reply.TaskRunID, agentcontract.TaskEventConnectorReplyFailed, connectorReplyEventBody(PlatformInboundEvent{MessageID: queuedReply.RawEventID}, queuedReply.Reply, queuedReply.OutboxID, "", errorValue.Error()))
	nextAttemptAt := nextConnectorAttemptAt(queuedReply.AttemptCount)
	if markError := connectorRuntime.outboxRepository().MarkConnectorReplyFailed(queuedReply, errorValue, nextAttemptAt); markError != nil {
		connectorRuntime.logger.Warn("connector."+queuedReply.Platform+".outbox.mark_failed_failed", slog.String("outboxID", queuedReply.OutboxID), slog.String("error", markError.Error()))
	}
}

func nextConnectorAttemptAt(attemptCount int) time.Time {
	if attemptCount >= connectorMaximumAttemptCount {
		return time.Time{}
	}
	delaySeconds := 1 << max(0, min(attemptCount, 6))
	return time.Now().UTC().Add(time.Duration(delaySeconds) * time.Second)
}

func sleepConnectorWorker(ctx context.Context) {
	timer := time.NewTimer(connectorWorkerIdleDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
