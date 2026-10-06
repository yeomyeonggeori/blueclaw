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

func rootMessage(messageID string, replyTargetID string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = "direct-1"
	event.ReplyTargetID = replyTargetID
	isThread := false
	event.IsThread = &isThread
	return event
}

func seedWaitingQuestionAtRoot(t *testing.T, connectorRuntime *ConnectorRuntime, rootMessageID string) task.TaskRun {
	t.Helper()
	running := seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "direct-1",
		ReplyTargetID:  rootMessageID,
	}, "clock me out")
	connectorRuntime.taskRunService.AppendTaskEvent(running.TaskRunID, agentcontract.TaskEventAskRequested, `{"kind":"input","question":"Which time should I record?"}`)
	waiting, errorValue := connectorRuntime.taskRunService.PauseTaskRun(running.TaskRunID, task.TaskStatusWaitingUserInput, "")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return waiting
}

func TestARootMessageDoesNotContinueAWaitingRunStartedAtRoot(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	taskWaitRepository := task.NewInMemoryTaskWaitTokenRepository()
	connectorRuntime.UseTaskWaitTokenRepository(taskWaitRepository)
	waiting := seedWaitingQuestionAtRoot(t, connectorRuntime, "message-clock-out")
	now := time.Now().UTC()
	if errorValue := taskWaitRepository.InsertTaskWaitToken(task.TaskWaitToken{
		WaitID:         "wait-clock-out",
		TaskRunID:      waiting.TaskRunID,
		PersonID:       "person-1",
		Platform:       "buzz",
		ConversationID: "direct-1",
		ReplyTargetID:  "dispatch-question",
		ThreadRootID:   "message-clock-out",
		Kind:           "input",
		State:          "open",
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	for _, root := range []PlatformInboundEvent{
		rootMessage("message-weather", "message-weather"),
		rootMessage("message-weather", "message-clock-out"),
	} {
		resolution := connectorRuntime.resolveInboundTaskWait("person-1", "buzz", root)
		if resolution.HasTaskWaitToken {
			t.Fatalf("a root message took the root-started run's wait: %+v", resolution)
		}
		if _, isFound := connectorRuntime.findPendingAskInteraction("person-1", "", root, resolution); isFound {
			t.Fatal("a root message was offered as the answer to the root-started run's question")
		}
		if _, isFound := connectorRuntime.findActiveGoal("person-1", "", root, resolution); isFound {
			t.Fatal("a root message inherited the root-started run's goal")
		}
	}

	replyInThread := threadReply("message-time", "message-clock-out")
	resolution := connectorRuntime.resolveInboundTaskWait("person-1", "buzz", replyInThread)
	if !resolution.HasTaskWaitToken || resolution.TaskWaitToken.TaskRunID != waiting.TaskRunID {
		t.Fatalf("a reply in the run's thread did not find its wait: %+v", resolution)
	}
	interaction, isFound := connectorRuntime.findPendingAskInteraction("person-1", "", replyInThread, resolution)
	if !isFound || interaction.TaskRunID != waiting.TaskRunID {
		t.Fatalf("a reply in the run's thread did not find its question: found=%v %+v", isFound, interaction)
	}
}

func TestARootMessageDoesNotSteerARunStartedAtRoot(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	running := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "direct-1",
		ReplyTargetID:  "message-report",
	}, "write the weekly report")

	if _, isFound := connectorRuntime.latestRunningConversationTask("person-1", rootMessage("message-lunch", "message-lunch")); isFound {
		t.Fatal("a root message was routed to the run another root message started")
	}
	found, isFound := connectorRuntime.latestRunningConversationTask("person-1", threadReply("message-shorter", "message-report"))
	if !isFound || found.TaskRunID != running.TaskRunID {
		t.Fatalf("a reply in the run's thread did not reach it: found=%v %+v", isFound, found)
	}
}
