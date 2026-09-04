package inboundengagement

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type stubAddressingClassifier struct {
	decision agentcontract.AddressingDecision
}

func (classifier stubAddressingClassifier) ClassifyAddressing(context.Context, agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error) {
	return classifier.decision, nil
}

func gateReturning(decision agentcontract.AddressingDecision) *Gate {
	return NewGate(stubAddressingClassifier{decision: decision}, nil, nil, nil)
}

func channelRequest() Request {
	return Request{Prompt: "이거 정리해줘", ConversationType: "O"}
}

func TestResolveIgnoresUninvitedAttachmentsOnly(t *testing.T) {
	gate := gateReturning(agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true})

	uninvitedRequest := Request{Prompt: "User attached file(s).", ConversationType: "O", AttachmentsOnly: true}
	decision := gate.Resolve(context.Background(), "mattermost", uninvitedRequest)
	if decision.ShouldLaunch {
		t.Fatalf("uninvited attachments-only channel post must be ignored, got %+v", decision)
	}
	if !strings.Contains(decision.IgnoreReason, "attachments_only") {
		t.Fatalf("expected attachments_only ignore reason, got %q", decision.IgnoreReason)
	}

	directRequest := Request{Prompt: "User attached file(s).", ConversationType: "D", AttachmentsOnly: true}
	if !gate.Resolve(context.Background(), "mattermost", directRequest).ShouldLaunch {
		t.Fatal("DM with only an attachment must still engage")
	}

	mentionRequest := Request{Prompt: "User attached file(s).", ConversationType: "O", AttachmentsOnly: true, BotMentioned: true}
	if !gate.Resolve(context.Background(), "mattermost", mentionRequest).ShouldLaunch {
		t.Fatal("bot-mentioned attachment-only post must still engage")
	}
}

func TestResolveReactOnly(t *testing.T) {
	gate := gateReturning(agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetAnyone, ShouldRespond: false, ReactionEmoji: "eyes"})

	decision := gate.Resolve(context.Background(), "mattermost", channelRequest())

	if decision.ShouldLaunch {
		t.Fatalf("react-only message must not launch a task, got %+v", decision)
	}
	if decision.ReactionEmoji != "eyes" {
		t.Fatalf("expected react-only emoji 'eyes', got %+v", decision)
	}
}

func TestResolveReactAndRespond(t *testing.T) {
	gate := gateReturning(agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true, ReactionEmoji: "+1"})

	decision := gate.Resolve(context.Background(), "mattermost", channelRequest())

	if !decision.ShouldLaunch || decision.ReactionEmoji != "+1" {
		t.Fatalf("expected react-and-respond (launch + emoji), got %+v", decision)
	}
}
