package inboundengagement

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

type gateReturning AddressingDecision

func (decision gateReturning) Resolve(ctx context.Context, platform string, request Request) Decision {
	return Resolve(ctx, slog.Default(), platform, request, func(context.Context) (AddressingDecision, error) {
		return AddressingDecision(decision), nil
	})
}

func channelRequest() Request {
	return Request{ConversationType: "O"}
}

func TestResolveIgnoresUninvitedAttachmentsOnly(t *testing.T) {
	gate := gateReturning{Target: AddressingTargetBot, ShouldRespond: true}

	uninvitedRequest := Request{ConversationType: "O", AttachmentsOnly: true}
	decision := gate.Resolve(context.Background(), "mattermost", uninvitedRequest)
	if decision.ShouldLaunch {
		t.Fatalf("uninvited attachments-only channel post must be ignored, got %+v", decision)
	}
	if !strings.Contains(decision.IgnoreReason, "attachments_only") {
		t.Fatalf("expected attachments_only ignore reason, got %q", decision.IgnoreReason)
	}

	directRequest := Request{ConversationType: "D", AttachmentsOnly: true}
	if !gate.Resolve(context.Background(), "mattermost", directRequest).ShouldLaunch {
		t.Fatal("DM with only an attachment must still engage")
	}

	mentionRequest := Request{ConversationType: "O", AttachmentsOnly: true, BotMentioned: true}
	if !gate.Resolve(context.Background(), "mattermost", mentionRequest).ShouldLaunch {
		t.Fatal("bot-mentioned attachment-only post must still engage")
	}
}

func TestResolveReactOnly(t *testing.T) {
	gate := gateReturning{Target: AddressingTargetAnyone, ShouldRespond: false, ReactionEmoji: "eyes"}

	decision := gate.Resolve(context.Background(), "mattermost", channelRequest())

	if decision.ShouldLaunch {
		t.Fatalf("react-only message must not launch a task, got %+v", decision)
	}
	if decision.ReactionEmoji != "eyes" {
		t.Fatalf("expected react-only emoji 'eyes', got %+v", decision)
	}
}

func TestResolveReactAndRespond(t *testing.T) {
	gate := gateReturning{Target: AddressingTargetBot, ShouldRespond: true, ReactionEmoji: "+1"}

	decision := gate.Resolve(context.Background(), "mattermost", channelRequest())

	if !decision.ShouldLaunch || decision.ReactionEmoji != "+1" {
		t.Fatalf("expected react-and-respond (launch + emoji), got %+v", decision)
	}
}
