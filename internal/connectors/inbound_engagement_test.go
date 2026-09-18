package connectors

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type intakeDecisionRecorder struct {
	lastRequest agentcontract.IntakeDecisionRequest
}

func (recorder *intakeDecisionRecorder) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, _ *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	recorder.lastRequest = request
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{
			MessageID:  message.MessageID,
			Addressing: agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true},
		})
	}
	return decisions, nil
}

func channelMentionEvent() PlatformInboundEvent {
	return PlatformInboundEvent{
		Platform: "mattermost",
		Prompt:   "이번 주 일정 정리해줘",
		Context: VisibleContext{
			ConversationType: "O",
			Addressing:       AddressingMetadata{BotMentioned: true},
		},
	}
}

func recordingIntakeDecisionRuntime(t *testing.T) (*ConnectorRuntime, *intakeDecisionRecorder, *testAdapter) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	recorder := &intakeDecisionRecorder{}
	connectorRuntime := NewConnectorRuntime(testConnectorIdentityService(), nil, taskRunService, task.NewTaskEventService(), nil)
	connectorRuntime.UseTaskRunService(taskRunService)
	connectorRuntime.UseIntakeDecider(recorder)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, recorder, adapter
}

func TestIntakeDecisionCarriesConfiguredAgentIdentity(t *testing.T) {
	connectorRuntime, recorder, adapter := recordingIntakeDecisionRuntime(t)
	connectorRuntime.UseAgentIdentityProvider(func() agentcontract.AgentIdentity {
		return agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"}
	})

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", withInboundDecision(channelMentionEvent()))

	if recorder.lastRequest.AgentIdentity.Name != "김인턴" || recorder.lastRequest.AgentIdentity.Handle != "internkim" {
		t.Fatalf("expected the configured agent identity to reach the intake decision, got %+v", recorder.lastRequest.AgentIdentity)
	}
}

func TestIntakeDecisionCarriesInboundEventFields(t *testing.T) {
	connectorRuntime, recorder, adapter := recordingIntakeDecisionRuntime(t)

	event := channelMentionEvent()
	event.MessageID = "message-1"
	event.RawReceivedAt = time.Unix(1756800000, 0)
	event.Context.Sender = VisibleContextSender{Name: "이샘플", Handle: "sample"}

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", withInboundDecision(event))

	if recorder.lastRequest.ConversationType != "O" {
		t.Fatalf("expected the conversation type to reach the intake decision, got %q", recorder.lastRequest.ConversationType)
	}
	if len(recorder.lastRequest.Messages) != 1 {
		t.Fatalf("expected one message to be decided, got %d", len(recorder.lastRequest.Messages))
	}
	message := recorder.lastRequest.Messages[0]
	if message.MessageID != "message-1" || message.Prompt != event.Prompt || !message.BotMentioned {
		t.Fatalf("expected the inbound event's message to reach the decision, got %+v", message)
	}
	if message.SenderName != "이샘플" || message.SenderHandle != "sample" || !message.SentAt.Equal(event.RawReceivedAt) {
		t.Fatalf("expected the sender and receipt time to reach the decision, got %+v", message)
	}
}

func TestIntakeDecisionWithoutIdentityProviderStaysEmpty(t *testing.T) {
	connectorRuntime, recorder, adapter := recordingIntakeDecisionRuntime(t)

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", withInboundDecision(channelMentionEvent()))

	if recorder.lastRequest.AgentIdentity != (agentcontract.AgentIdentity{}) {
		t.Fatalf("expected an empty agent identity without a provider, got %+v", recorder.lastRequest.AgentIdentity)
	}
}

func TestOneMessageIsDecidedOnce(t *testing.T) {
	connectorRuntime, _, adapter := recordingIntakeDecisionRuntime(t)
	countingDecider := &countingIntakeDecider{}
	connectorRuntime.UseIntakeDecider(countingDecider)
	event := withInboundDecision(channelMentionEvent())

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", event)
	connectorRuntime.decidedTurnFields(context.Background(), adapter, event)
	connectorRuntime.relatesToActiveTask(context.Background(), adapter, event)

	if countingDecider.callCount != 1 {
		t.Fatalf("expected the gate, the router and the follow-up check to share one decision call, got %d", countingDecider.callCount)
	}
}

type countingIntakeDecider struct {
	callCount int
}

func (decider *countingIntakeDecider) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, _ *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	decider.callCount++
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{MessageID: message.MessageID})
	}
	return decisions, nil
}
