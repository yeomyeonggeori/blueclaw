package connectors

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func messageSendEvents(targetField string, targetValue string, isFailure bool) []agentcontract.TaskEvent {
	resultBody := `{"toolName":"message_send","output":{"content":"보냈습니다"}}`
	if isFailure {
		resultBody = `{"toolName":"message_send","failure":{"code":"operation_failed"}}`
	}
	return []agentcontract.TaskEvent{
		{Name: "tool.message_send.requested", Body: `{"toolName":"message_send","input":{"` + targetField + `":"` + targetValue + `","body":"살아있어요"}}`},
		{Name: "tool.message_send.result", Body: resultBody},
	}
}

func TestTheRuntimeStaysQuietWhenTheAgentAlreadyAnsweredThere(t *testing.T) {
	if !agentAlreadyDeliveredTo(messageSendEvents("conversationID", "thread:abc", false), []string{"thread:abc"}) {
		t.Fatal("expected an answer already delivered to the place the runtime is about to deliver to, to stop it saying the same thing twice")
	}
}

func TestAScheduledRunComparesAgainstWhereItActuallyDelivers(t *testing.T) {
	scheduledDeliveryTargets := []string{"schedule:daily-brief", "direct-1", "reply-target-1"}

	if !agentAlreadyDeliveredTo(messageSendEvents("conversationID", "direct-1", false), scheduledDeliveryTargets) {
		t.Fatal("expected a scheduled run to compare against the conversation it delivers to, not the synthetic schedule id it runs under")
	}
}

func TestPostingToAChannelStillLeavesTheRequesterTheirAnswer(t *testing.T) {
	if agentAlreadyDeliveredTo(messageSendEvents("channelID", "channel:general", false), []string{"thread:abc"}) {
		t.Fatal("expected posting somewhere else to leave the requester's own answer alone, because that is a different act")
	}
}

func TestASendThatFailedLeavesTheAnswerToTheRuntime(t *testing.T) {
	if agentAlreadyDeliveredTo(messageSendEvents("conversationID", "thread:abc", true), []string{"thread:abc"}) {
		t.Fatal("expected a send that failed to leave the requester with an answer rather than silence")
	}
}

func TestAnUnrecognisedSendShapeStillDelivers(t *testing.T) {
	events := []agentcontract.TaskEvent{
		{Name: "tool.message_send.requested", Body: `{"toolName":"message_send","input":{"recipientHint":"김예시"}}`},
		{Name: "tool.message_send.result", Body: `{"toolName":"message_send","output":{}}`},
	}

	if agentAlreadyDeliveredTo(events, []string{"thread:abc"}) {
		t.Fatal("expected a send this check cannot read to fall back to delivering, because a duplicate is recoverable and silence is not")
	}
}

func TestATaskThatSentNothingStillReplies(t *testing.T) {
	if agentAlreadyDeliveredTo([]agentcontract.TaskEvent{{Name: "task.completed", Body: "{}"}}, []string{"thread:abc"}) {
		t.Fatal("expected an ordinary task to reply")
	}
}
