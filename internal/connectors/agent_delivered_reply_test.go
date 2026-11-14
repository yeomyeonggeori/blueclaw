package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

var threadBeingAnswered = VisibleContext{ConversationType: "channel", ChannelID: "channel:general"}

func sendEvents(sideEffectClass string, input string, resultBody string) []agentcontract.TaskEvent {
	return []agentcontract.TaskEvent{
		{Name: "tool.message_send.requested", Body: `{"observationID":"host-1","toolName":"message_send","sideEffectClass":"` + sideEffectClass + `","input":` + input + `}`},
		{Name: "tool.message_send.result", Body: resultBody},
	}
}

const sentResult = `{"observationID":"host-1","summary":"보냈습니다"}`

func TestTheRuntimeStaysQuietWhenTheAgentAlreadyAnsweredInTheThread(t *testing.T) {
	events := sendEvents("external_send", `{"targetType":"currentThread","message":"살아있어요"}`, sentResult)

	if !agentAnsweredTheConversation(events, threadBeingAnswered) {
		t.Fatal("expected an answer the agent already sent to the thread being answered to stop the runtime saying the same thing twice")
	}
}

func TestPostingToTheSameChannelByItsIDCountsAsAnswering(t *testing.T) {
	events := sendEvents("external_send", `{"targetType":"channel","channelID":"channel:general","message":"살아있어요"}`, sentResult)

	if !agentAnsweredTheConversation(events, threadBeingAnswered) {
		t.Fatal("expected a post to the channel being answered, named by its ID, to count as the answer")
	}
}

func TestPostingToAnotherChannelStillLeavesTheRequesterTheirAnswer(t *testing.T) {
	events := sendEvents("external_send", `{"targetType":"channel","channelID":"channel:qa","message":"확인 부탁드려요"}`, sentResult)

	if agentAnsweredTheConversation(events, threadBeingAnswered) {
		t.Fatal("expected posting somewhere else to leave the requester's own answer alone, because that is a different act")
	}
}

func TestASendThatFailedOrIsHeldLeavesTheAnswerToTheRuntime(t *testing.T) {
	for _, resultBody := range []string{
		`{"observationID":"host-1","failure":{"kind":"unknown","code":"operation_failed"}}`,
		`{"observationID":"host-1","failure":{"kind":"policy","code":"approval_required","requiresApproval":true}}`,
	} {
		events := sendEvents("external_send", `{"targetType":"currentThread","message":"살아있어요"}`, resultBody)
		if agentAnsweredTheConversation(events, threadBeingAnswered) {
			t.Fatalf("expected a send that did not go out to leave the requester with an answer rather than silence, got quiet for %s", resultBody)
		}
	}
}

func TestACallThatSendsNothingOutIsNotAnAnswer(t *testing.T) {
	events := sendEvents("write", `{"targetType":"currentThread"}`, sentResult)

	if agentAnsweredTheConversation(events, threadBeingAnswered) {
		t.Fatal("expected only a call that sends something out to count as answering, whatever its input looks like")
	}
}

func TestATaskThatSentNothingStillReplies(t *testing.T) {
	if agentAnsweredTheConversation([]agentcontract.TaskEvent{{Name: "task.completed", Body: "{}"}}, threadBeingAnswered) {
		t.Fatal("expected an ordinary task to reply")
	}
}
