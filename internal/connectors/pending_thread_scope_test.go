package connectors

import (
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func seedWaitingApprovalInThread(t *testing.T, connectorRuntime *ConnectorRuntime, threadRootID string) task.TaskRun {
	t.Helper()
	running := seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "direct-1",
		ReplyTargetID:  threadRootID,
		IsThread:       true,
	}, "delete the 11:00 event")
	waiting, errorValue := connectorRuntime.taskRunService.PauseTaskRun(running.TaskRunID, task.TaskStatusWaitingApproval, "")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return waiting
}

func threadReply(messageID string, threadRootID string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = "direct-1"
	event.ReplyTargetID = threadRootID
	isThread := true
	event.IsThread = &isThread
	return event
}

func TestAReplyInAnotherThreadDoesNotAnswerThisThreadsApproval(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	waiting := seedWaitingApprovalInThread(t, connectorRuntime, "thread-delete")

	if _, isFound := connectorRuntime.findPendingApproval("person-1", "", threadReply("message-other", "thread-attendance"), inboundTaskWaitResolution{}); isFound {
		t.Fatal("a reply in the attendance thread was taken as the answer to the delete thread's approval")
	}
	approval, isFound := connectorRuntime.findPendingApproval("person-1", "", threadReply("message-yes", "thread-delete"), inboundTaskWaitResolution{})
	if !isFound || approval.TaskRun.TaskRunID != waiting.TaskRunID {
		t.Fatalf("a reply in the delete thread did not find its approval: found=%v %+v", isFound, approval.TaskRun)
	}
}

func TestWorkInAnotherThreadDoesNotCountAsAnExchangeAfterTheQuestion(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	waiting := seedWaitingApprovalInThread(t, connectorRuntime, "thread-delete")
	askedAt := time.Now()
	time.Sleep(time.Millisecond)
	seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "direct-1",
		ReplyTargetID:  "thread-attendance",
		IsThread:       true,
	}, "did I clock out?")

	turn := &inboundTurn{personID: "person-1", event: threadReply("message-yes", "thread-delete")}
	if exchanges := connectorRuntime.exchangesSince(turn, askedAt, waiting.TaskRunID); exchanges != 0 {
		t.Fatalf("a task in another thread counted as %d exchanges after the delete thread's question", exchanges)
	}
}
