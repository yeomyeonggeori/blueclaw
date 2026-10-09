package acpsession

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
)

type reactingAdapter struct {
	mutex     sync.Mutex
	reactions []connectors.ReactionTarget
}

func (adapter *reactingAdapter) Name() string { return "buzz" }

func (adapter *reactingAdapter) ParseHTTPEvent(context.Context, *http.Request) (connectors.HTTPParseResult, error) {
	return connectors.HTTPParseResult{}, errors.New("this adapter parses nothing")
}

func (adapter *reactingAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{}, errors.New("this adapter resolves nobody")
}

func (adapter *reactingAdapter) StartProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *reactingAdapter) StopProgress(context.Context, connectors.ReplyTarget) error {
	return nil
}

func (adapter *reactingAdapter) SendReply(context.Context, connectors.ReplyTarget, connectors.OutboundReply) (string, error) {
	return "", errors.New("a session turn answers through the session, never through the adapter")
}

func (adapter *reactingAdapter) FetchHistory(context.Context, string, int) (connectors.VisibleContext, error) {
	return connectors.VisibleContext{}, nil
}

func (adapter *reactingAdapter) AddReaction(_ context.Context, target connectors.ReactionTarget) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.reactions = append(adapter.reactions, target)
	return nil
}

func promptDirectMessage(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, messageID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("9시 10분")},
		Meta: map[string]any{MessageMetaKey: map[string]any{
			"messageID": messageID,
			"context":   map[string]any{"conversationType": "dm"},
		}},
	})
	if errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
}
