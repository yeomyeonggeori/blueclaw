//go:build !nobundledharness

package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func threadReply(messageID string, threadRootID string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = "direct-1"
	event.ReplyTargetID = threadRootID
	isThread := true
	event.IsThread = &isThread
	return event
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

	if _, isFound := connectorRuntime.findPendingAskInteraction("person-1", messageThatNamesNoThreadFlag("message-deploy-reply", "thread-deploy")); isFound {
		t.Fatal("a reply in the deploy thread was offered as the answer to the patent thread's question")
	}
	if _, isFound := connectorRuntime.findPendingAskInteraction("person-1", messageThatNamesNoThreadFlag("thread-deploy", "thread-deploy")); isFound {
		t.Fatal("a new top-level message was offered as the answer to the patent thread's question")
	}
	interaction, isFound := connectorRuntime.findPendingAskInteraction("person-1", messageThatNamesNoThreadFlag("message-patent-reply", "thread-patent"))
	if !isFound || interaction.TaskRunID != waiting.TaskRunID {
		t.Fatalf("a reply in the patent thread did not find its question: found=%v %+v", isFound, interaction)
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
	waiting := seedWaitingQuestionAtRoot(t, connectorRuntime, "message-clock-out")

	for _, root := range []PlatformInboundEvent{
		rootMessage("message-weather", "message-weather"),
		rootMessage("message-weather", "message-clock-out"),
	} {
		if _, isFound := connectorRuntime.findPendingAskInteraction("person-1", root); isFound {
			t.Fatal("a root message was offered as the answer to the root-started run's question")
		}
		if _, isFound := connectorRuntime.findActiveGoal("person-1", root); isFound {
			t.Fatal("a root message inherited the root-started run's goal")
		}
	}

	interaction, isFound := connectorRuntime.findPendingAskInteraction("person-1", threadReply("message-time", "message-clock-out"))
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
