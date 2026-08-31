package agentruntime

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestResolveActiveCircleIDUsesChannelOrMention(t *testing.T) {
	channelCircleID, hasChannelConflict := ResolveActiveCircleID(ToolCatalogRequest{
		ConversationChannelName: "circle-hr-compensation",
		PersonAccess:            policy.PersonAccess{Circles: []string{"member", "hr-compensation"}},
	})
	mentionedCircleID, hasMentionConflict := ResolveActiveCircleID(ToolCatalogRequest{
		Prompt:       "please remember this for @hr-compensation",
		PersonAccess: policy.PersonAccess{Circles: []string{"member", "hr-compensation"}},
	})

	if channelCircleID != "hr-compensation" || hasChannelConflict {
		t.Fatalf("expected channel active circle, got %q conflict=%v", channelCircleID, hasChannelConflict)
	}
	if mentionedCircleID != "hr-compensation" || hasMentionConflict {
		t.Fatalf("expected mention active circle, got %q conflict=%v", mentionedCircleID, hasMentionConflict)
	}
}

func TestResolveActiveCircleIDIgnoresInaccessibleMention(t *testing.T) {
	circleID, hasConflict := ResolveActiveCircleID(ToolCatalogRequest{
		Prompt:       "please remember this for @hr-compensation",
		PersonAccess: policy.PersonAccess{Circles: []string{"member"}},
	})

	if circleID != "" || hasConflict {
		t.Fatalf("expected inaccessible mention to be ignored, got %q conflict=%v", circleID, hasConflict)
	}
}
