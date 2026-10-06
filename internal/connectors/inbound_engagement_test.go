//go:build !nobundledharness

package connectors

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

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

func recordingGatewayDecisionRuntime(t *testing.T) (*ConnectorRuntime, *scriptedGatewayDecider, *testAdapter) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	recorder := &scriptedGatewayDecider{addressing: addressedToBot()}
	connectorRuntime := NewConnectorRuntime(testConnectorIdentityService(), nil, taskRunService, task.NewTaskEventService(), nil)
	connectorRuntime.UseTaskRunService(taskRunService)
	connectorRuntime.UseGatewayDecider(recorder)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, recorder, adapter
}

func TestGatewayDecisionCarriesConfiguredAgentIdentity(t *testing.T) {
	connectorRuntime, recorder, adapter := recordingGatewayDecisionRuntime(t)
	connectorRuntime.UseAgentIdentityProvider(func() agentcontract.AgentIdentity {
		return agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"}
	})

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", withGatewayDecision(channelMentionEvent()))

	if recorder.lastFacts().AgentIdentity.Name != "김인턴" || recorder.lastFacts().AgentIdentity.Handle != "internkim" {
		t.Fatalf("expected the configured agent identity to reach the intake decision, got %+v", recorder.lastFacts().AgentIdentity)
	}
}

func TestGatewayDecisionCarriesInboundEventFields(t *testing.T) {
	connectorRuntime, recorder, adapter := recordingGatewayDecisionRuntime(t)

	event := channelMentionEvent()
	event.MessageID = "message-1"
	event.RawReceivedAt = time.Unix(1756800000, 0)
	event.Context.Sender = VisibleContextSender{Name: "이샘플", Handle: "sample"}

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", withGatewayDecision(event))

	if recorder.lastFacts().ConversationType != "O" {
		t.Fatalf("expected the conversation type to reach the intake decision, got %q", recorder.lastFacts().ConversationType)
	}
	if len(recorder.lastFacts().Messages) != 1 {
		t.Fatalf("expected one message to be decided, got %d", len(recorder.lastFacts().Messages))
	}
	message := recorder.lastFacts().Messages[0]
	if message.MessageID != "message-1" || message.Prompt != event.Prompt || !message.BotMentioned {
		t.Fatalf("expected the inbound event's message to reach the decision, got %+v", message)
	}
	if message.SenderName != "이샘플" || message.SenderHandle != "sample" || !message.SentAt.Equal(event.RawReceivedAt) {
		t.Fatalf("expected the sender and receipt time to reach the decision, got %+v", message)
	}
}

func TestGatewayDecisionWithoutIdentityProviderStaysEmpty(t *testing.T) {
	connectorRuntime, recorder, adapter := recordingGatewayDecisionRuntime(t)

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", withGatewayDecision(channelMentionEvent()))

	if recorder.lastFacts().AgentIdentity != (agentcontract.AgentIdentity{}) {
		t.Fatalf("expected an empty agent identity without a provider, got %+v", recorder.lastFacts().AgentIdentity)
	}
}

func TestOneMessageIsDecidedOnce(t *testing.T) {
	connectorRuntime, _, adapter := recordingGatewayDecisionRuntime(t)
	countingDecider := &scriptedGatewayDecider{}
	connectorRuntime.UseGatewayDecider(countingDecider)
	event := withGatewayDecision(channelMentionEvent())

	connectorRuntime.resolveInboundEngagement(context.Background(), adapter, "mattermost", event)
	connectorRuntime.judgeInboundMessage(context.Background(), adapter, event)
	connectorRuntime.relatesToActiveTask(context.Background(), adapter, event)

	if countingDecider.calls() != 1 {
		t.Fatalf("expected the gate, the router and the follow-up check to share one decision call, got %d", countingDecider.calls())
	}
}
