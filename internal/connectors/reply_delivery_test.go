//go:build !nobundledharness

package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func countConnectorTaskEvents(connectorRuntime *ConnectorRuntime, taskRunID string, name string) int {
	count := 0
	for _, taskEvent := range connectorRuntime.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == name {
			count++
		}
	}
	return count
}

func waitingQuestionResult(waiting task.TaskRun) agentcontract.AgentTurnResult {
	return agentcontract.AgentTurnResult{TaskRun: waiting, UserNotice: "Which time should I record?"}
}

func TestAQuestionASessionTurnDeliversIsRecordedAsSentOnceWhereAnOutboxExists(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	waiting := seedWaitingQuestionAtRoot(t, connectorRuntime, "message-clock-out")
	sessionMessages := []string{}
	sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), rootMessage("message-clock-out", "message-clock-out"), "person-1", func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
		sessionMessages = append(sessionMessages, reply.Message)
		return "", nil
	})

	if errorValue := sessionTurn.DeliverReply(context.Background(), waitingQuestionResult(waiting)); errorValue != nil {
		t.Fatal(errorValue)
	}

	if len(sessionMessages) != 1 || len(repository.pendingReplies) != 0 {
		t.Fatalf("the question did not go out through the session alone: session=%v outbox=%d", sessionMessages, len(repository.pendingReplies))
	}
	if sent := countConnectorTaskEvents(connectorRuntime, waiting.TaskRunID, agentcontract.TaskEventConnectorReplySent); sent != 1 {
		t.Fatalf("the question the session sent is recorded as sent %d times, want once", sent)
	}
}

func TestAQuestionTheOutboxSendsIsRecordedOnce(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	waiting := seedWaitingQuestionAtRoot(t, connectorRuntime, "message-clock-out")
	event := rootMessage("message-clock-out", "message-clock-out")
	replyTarget, _ := connectorRuntime.buildReplyTarget(context.Background(), adapter, event)

	if _, isSent := connectorRuntime.sendUserNoticeReply(withConnectorEvent(context.Background(), event), adapter.Name(), event, waiting.TaskRunID, replyTarget, waitingQuestionResult(waiting), connectorRuntime.enqueueConnectorReply); !isSent {
		t.Fatal("the question was not handed to the outbox")
	}
	if sent := countConnectorTaskEvents(connectorRuntime, waiting.TaskRunID, agentcontract.TaskEventConnectorReplySent); sent != 0 {
		t.Fatalf("a question still in the outbox is recorded as sent %d times", sent)
	}
	if !connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
		t.Fatal("the outbox did not send the question")
	}

	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected the outbox to send the question once, got %+v", adapter.sentReplies)
	}
	if sent := countConnectorTaskEvents(connectorRuntime, waiting.TaskRunID, agentcontract.TaskEventConnectorReplySent); sent != 1 {
		t.Fatalf("the question the outbox sent is recorded as sent %d times, want once", sent)
	}
}
