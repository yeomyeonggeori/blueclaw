package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type addressingRequestRecorder struct {
	lastRequest agentcontract.AddressingClassificationRequest
}

func (recorder *addressingRequestRecorder) ClassifyAddressing(ctx context.Context, request agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error) {
	recorder.lastRequest = request
	return agentcontract.AddressingDecision{ShouldRespond: true}, nil
}

func (recorder *addressingRequestRecorder) ClassifyActiveTaskFollowUp(context.Context, agentcontract.ActiveTaskFollowUpClassificationRequest) (bool, error) {
	return false, nil
}

func channelMentionEvent() PlatformInboundEvent {
	return PlatformInboundEvent{
		Prompt: "이번 주 일정 정리해줘",
		Context: VisibleContext{
			ConversationType: "O",
			Addressing:       AddressingMetadata{BotMentioned: true},
		},
	}
}

func TestAddressingClassificationCarriesConfiguredAgentIdentity(t *testing.T) {
	recorder := &addressingRequestRecorder{}
	connectorRuntime := NewConnectorRuntime(nil, nil, nil, nil, nil)
	connectorRuntime.UseIntakeClassifier(recorder)
	connectorRuntime.UseAgentIdentityProvider(func() agentcontract.AgentIdentity {
		return agentcontract.AgentIdentity{Name: "김인턴", Handle: "internkim"}
	})

	connectorRuntime.resolveInboundEngagement(context.Background(), "mattermost", channelMentionEvent())

	if recorder.lastRequest.AgentIdentity.Name != "김인턴" || recorder.lastRequest.AgentIdentity.Handle != "internkim" {
		t.Fatalf("expected the configured agent identity to reach the addressing classifier, got %+v", recorder.lastRequest.AgentIdentity)
	}
}

func TestAddressingClassificationWithoutIdentityProviderStaysEmpty(t *testing.T) {
	recorder := &addressingRequestRecorder{}
	connectorRuntime := NewConnectorRuntime(nil, nil, nil, nil, nil)
	connectorRuntime.UseIntakeClassifier(recorder)

	connectorRuntime.resolveInboundEngagement(context.Background(), "mattermost", channelMentionEvent())

	if recorder.lastRequest.AgentIdentity != (agentcontract.AgentIdentity{}) {
		t.Fatalf("expected an empty agent identity without a provider, got %+v", recorder.lastRequest.AgentIdentity)
	}
}
