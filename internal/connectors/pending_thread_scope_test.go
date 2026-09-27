package connectors

import (
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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

func seedWaitingQuestionInThread(t *testing.T, connectorRuntime *ConnectorRuntime, threadRootID string) task.TaskRun {
	t.Helper()
	running := seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "direct-1",
		ReplyTargetID:  threadRootID,
		IsThread:       true,
	}, "the patent draft review is done")
	connectorRuntime.taskRunService.AppendTaskEvent(running.TaskRunID, agentcontract.TaskEventAskRequested, `{"kind":"input","question":"Set a Monday reminder to send it?"}`)
	waiting, errorValue := connectorRuntime.taskRunService.PauseTaskRun(running.TaskRunID, task.TaskStatusWaitingUserInput, "")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return waiting
}

func messageThatNamesNoThreadFlag(messageID string, replyTargetID string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = "direct-1"
	event.ReplyTargetID = replyTargetID
	event.IsThread = nil
	return event
}

func TestAReplyInAnotherThreadDoesNotAnswerThisThreadsQuestionWhenThePlatformSendsNoThreadFlag(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	waiting := seedWaitingQuestionInThread(t, connectorRuntime, "thread-patent")

	if _, isFound := connectorRuntime.findPendingAskInteraction("person-1", "", messageThatNamesNoThreadFlag("message-deploy-reply", "thread-deploy"), inboundTaskWaitResolution{}); isFound {
		t.Fatal("a reply in the deploy thread was offered as the answer to the patent thread's question")
	}
	if _, isFound := connectorRuntime.findPendingAskInteraction("person-1", "", messageThatNamesNoThreadFlag("thread-deploy", "thread-deploy"), inboundTaskWaitResolution{}); isFound {
		t.Fatal("a new top-level message was offered as the answer to the patent thread's question")
	}
	interaction, isFound := connectorRuntime.findPendingAskInteraction("person-1", "", messageThatNamesNoThreadFlag("message-patent-reply", "thread-patent"), inboundTaskWaitResolution{})
	if !isFound || interaction.TaskRunID != waiting.TaskRunID {
		t.Fatalf("a reply in the patent thread did not find its question: found=%v %+v", isFound, interaction)
	}
}

func TestTheOnlyOpenWaitInTheConversationIsNotTakenFromAnotherThread(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	taskWaitRepository := task.NewInMemoryTaskWaitTokenRepository()
	connectorRuntime.UseTaskWaitTokenRepository(taskWaitRepository)
	waiting := seedWaitingQuestionInThread(t, connectorRuntime, "thread-patent")
	now := time.Now().UTC()
	if errorValue := taskWaitRepository.InsertTaskWaitToken(task.TaskWaitToken{
		WaitID:         "wait-patent",
		TaskRunID:      waiting.TaskRunID,
		PersonID:       "person-1",
		Platform:       "buzz",
		ConversationID: "direct-1",
		ReplyTargetID:  "dispatch-question",
		ThreadRootID:   "thread-patent",
		Kind:           "ask",
		State:          "open",
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	if resolution := connectorRuntime.resolveInboundTaskWait("person-1", "buzz", messageThatNamesNoThreadFlag("message-deploy-reply", "thread-deploy")); resolution.HasTaskWaitToken {
		t.Fatalf("the patent thread's wait was taken by a message in the deploy thread: %+v", resolution)
	}
	if resolution := connectorRuntime.resolveInboundTaskWait("person-1", "buzz", messageThatNamesNoThreadFlag("message-patent-reply", "thread-patent")); !resolution.HasTaskWaitToken {
		t.Fatal("a reply in the patent thread did not find its wait")
	}
}
