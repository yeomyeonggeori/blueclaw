package acpsession

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type consumingLauncher struct{}

func (consumingLauncher) Launch(context.Context, agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchResult, error) {
	return agentruntime.TaskLaunchResult{TurnResult: agentcontract.AgentTurnResult{
		TaskRun:           agentcontract.TaskRun{TaskRunID: "task-consumed", Status: agentcontract.TaskStatusCompleted, Result: "consumed"},
		TurnRoute:         agentcontract.TurnRouteConsume,
		ReactionEmojiName: "thumbsup",
		FinishMessage:     "알겠습니다.",
		ReplySuppressed:   true,
	}}, nil
}

type reactingAdapter struct {
	mutex     sync.Mutex
	reactions []connectors.ReactionTarget
}

func (adapter *reactingAdapter) Name() string { return "buzz" }

func (adapter *reactingAdapter) ParseHTTPEvent(context.Context, *http.Request) (connectors.HTTPParseResult, error) {
	return connectors.HTTPParseResult{}, errors.New("this adapter parses nothing")
}

func (adapter *reactingAdapter) ParseRealtimeEvent(context.Context, []byte, string) (connectors.PlatformInboundEvent, bool, error) {
	return connectors.PlatformInboundEvent{}, false, errors.New("this adapter parses nothing")
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

func TestAConsumedMessageIsAcknowledgedOnTheMessageItself(t *testing.T) {
	adapter := &reactingAdapter{}
	connectorRuntime := connectorRuntimeForTest(nil)
	connectorRuntime.RegisterAdapter(adapter)
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: consumingLauncher{},
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
		SessionTurns: connectorRuntime,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptDirectMessage(t, connection, sessionID, "message-answer")

	if len(adapter.reactions) != 1 || adapter.reactions[0].MessageID != "message-answer" || adapter.reactions[0].EmojiName != "thumbsup" {
		t.Fatalf("the consumed message got reactions %+v, expected thumbsup on message-answer", adapter.reactions)
	}
	if len(client.messages) != 0 {
		t.Fatalf("a message acknowledged with a reaction was also answered with %q", client.messages)
	}
}

func TestAConsumedDirectMessageNothingCanReactToIsAnsweredInWords(t *testing.T) {
	client := &recordingClient{}
	connection, _ := connectedPairWithCollaborators(t, client, Collaborators{
		TaskLauncher: consumingLauncher{},
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptDirectMessage(t, connection, sessionID, "message-answer")

	if len(client.messages) != 1 || client.messages[0] != "알겠습니다." {
		t.Fatalf("the requester was told %q, expected the reply the consuming turn wrote", client.messages)
	}
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
