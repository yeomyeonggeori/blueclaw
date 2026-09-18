package connectors

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func burstQueuedEvent(messageID string, conversationID string, senderID string, receivedAt time.Time) QueuedConnectorEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = conversationID
	event.SenderID = senderID
	event.RawReceivedAt = receivedAt
	return QueuedConnectorEvent{Event: event}
}

func TestABurstFromOneSenderIsDecidedInOneCall(t *testing.T) {
	connectorRuntime, _, adapter := recordingIntakeDecisionRuntime(t)
	recorder := &burstIntakeDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(2*time.Second)),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt.Add(4*time.Second)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recorder.requests) != 1 {
		t.Fatalf("expected one decision call for the burst, got %d", len(recorder.requests))
	}
	if len(recorder.requests[0].Messages) != 3 {
		t.Fatalf("expected the burst's three messages in one request, got %+v", recorder.requests[0].Messages)
	}
	for _, queuedEvent := range queuedEvents {
		decision, errorValue := connectorRuntime.decideInboundMessage(context.Background(), adapter, queuedEvent.Event)
		if errorValue != nil {
			t.Fatalf("expected %s to read the burst decision: %v", queuedEvent.Event.MessageID, errorValue)
		}
		if decision.MessageID != queuedEvent.Event.MessageID {
			t.Fatalf("expected each message to read its own answer, got %+v", decision)
		}
	}
	if len(recorder.requests) != 1 {
		t.Fatalf("expected the burst decision to answer for every message without a second call, got %d", len(recorder.requests))
	}
}

func TestSendersAndConversationsAreDecidedApart(t *testing.T) {
	connectorRuntime, _, _ := recordingIntakeDecisionRuntime(t)
	recorder := &burstIntakeDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "other-user", receivedAt),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-4", "channel-1", "sender-user", receivedAt),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recorder.requests) != 1 {
		t.Fatalf("expected only the one sender's pair to be batched, got %d calls", len(recorder.requests))
	}
	if len(recorder.requests[0].Messages) != 2 {
		t.Fatalf("expected one sender's two messages, got %+v", recorder.requests[0].Messages)
	}
}

func TestAMessageOutsideTheBurstWindowIsDecidedOnItsOwn(t *testing.T) {
	connectorRuntime, _, _ := recordingIntakeDecisionRuntime(t)
	recorder := &burstIntakeDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(time.Second)),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt.Add(time.Hour)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recorder.requests) != 1 {
		t.Fatalf("expected the backlogged message to be left alone, got %d calls", len(recorder.requests))
	}
	if len(recorder.requests[0].Messages) != 2 {
		t.Fatalf("expected the two messages that arrived together, got %+v", recorder.requests[0].Messages)
	}
}

type burstIntakeDecider struct {
	requests []agentcontract.IntakeDecisionRequest
}

func (decider *burstIntakeDecider) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, _ *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	decider.requests = append(decider.requests, request)
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{
			MessageID:  message.MessageID,
			Addressing: addressedToBot(),
		})
	}
	return decisions, nil
}
