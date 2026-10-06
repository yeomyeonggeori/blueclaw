package acpsession

import (
	"context"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type progressRecordingAdapter struct {
	reactingAdapter
	progressMutex sync.Mutex
	started       []connectors.ReplyTarget
	stopped       []connectors.ReplyTarget
}

func (adapter *progressRecordingAdapter) StartProgress(_ context.Context, replyTarget connectors.ReplyTarget) error {
	adapter.progressMutex.Lock()
	defer adapter.progressMutex.Unlock()
	adapter.started = append(adapter.started, replyTarget)
	return nil
}

func (adapter *progressRecordingAdapter) StopProgress(_ context.Context, replyTarget connectors.ReplyTarget) error {
	adapter.progressMutex.Lock()
	defer adapter.progressMutex.Unlock()
	adapter.stopped = append(adapter.stopped, replyTarget)
	return nil
}

func (adapter *progressRecordingAdapter) isShowing() bool {
	adapter.progressMutex.Lock()
	defer adapter.progressMutex.Unlock()
	return len(adapter.started) > len(adapter.stopped)
}

type progressWitnessingLauncher struct {
	*recordingLauncher
	adapter            *progressRecordingAdapter
	wasShowingAtLaunch bool
}

func (launcher *progressWitnessingLauncher) Launch(ctx context.Context, request agentruntime.TaskLaunchRequest) (agentruntime.TaskLaunchResult, error) {
	launcher.wasShowingAtLaunch = launcher.adapter.isShowing()
	return launcher.recordingLauncher.Launch(ctx, request)
}

func progressRecordingConnectorRuntime() (*connectors.ConnectorRuntime, *progressRecordingAdapter) {
	adapter := &progressRecordingAdapter{}
	connectorRuntime := connectorRuntimeForTest(nil)
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter
}

func TestATurnShowsItsConversationThatTheAgentIsWorkingUntilItAnswers(t *testing.T) {
	for _, conversationType := range []string{"dm", "channel"} {
		t.Run(conversationType, func(t *testing.T) {
			connectorRuntime, adapter := progressRecordingConnectorRuntime()
			connectorRuntime.UseGatewayDecider(addressedToTheAgent{})
			launcher := &progressWitnessingLauncher{recordingLauncher: &recordingLauncher{reply: "확인했습니다"}, adapter: adapter}
			connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
				TaskLauncher: launcher,
				Directory:    staticDirectory{},
				ReplyReader:  scriptedReader{},
				SessionTurns: connectorRuntime,
			})
			sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

			promptInConversation(t, connection, sessionID, map[string]any{"conversationType": conversationType})

			if !launcher.wasShowingAtLaunch {
				t.Fatal("the turn ran without the conversation being shown that the agent is working")
			}
			if len(adapter.started) != 1 {
				t.Fatalf("progress was shown %d times for one message, expected once", len(adapter.started))
			}
			shown := adapter.started[0]
			if shown.ConversationID != "conversation-1" || shown.ReplyTargetID != "reply-target-1" || shown.AnsweringMessageID != "message-1" {
				t.Fatalf("progress was shown on %+v, expected conversation-1 at reply-target-1 for message-1", shown)
			}
			if adapter.isShowing() {
				t.Fatal("progress is still shown after the turn answered")
			}
		})
	}
}

func TestAMessageTheAgentIgnoresShowsNoProgress(t *testing.T) {
	connectorRuntime, adapter := progressRecordingConnectorRuntime()
	connectorRuntime.UseGatewayDecider(addressedToSomebodyElse{})
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher: &recordingLauncher{},
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
		SessionTurns: connectorRuntime,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptInConversation(t, connection, sessionID, map[string]any{"conversationType": "channel"})

	if len(adapter.started) != 0 {
		t.Fatalf("a message addressed to somebody else showed progress %d times", len(adapter.started))
	}
}

type progressWitnessingDecider struct {
	addressedToSomebodyElse
	adapter              *progressRecordingAdapter
	wasShowingAtDecision bool
	hasDecided           bool
}

func (decider *progressWitnessingDecider) Decide(ctx context.Context, facts inboundengagement.Facts, observe agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	decider.wasShowingAtDecision = decider.adapter.isShowing()
	decider.hasDecided = true
	return decider.addressedToSomebodyElse.Decide(ctx, facts, observe)
}

func TestAMentionInARoomShowsProgressWhileTheAgentDecidesWhetherToAnswer(t *testing.T) {
	connectorRuntime, adapter := progressRecordingConnectorRuntime()
	decider := &progressWitnessingDecider{adapter: adapter}
	connectorRuntime.UseGatewayDecider(decider)
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher: &recordingLauncher{},
		Directory:    staticDirectory{},
		ReplyReader:  scriptedReader{},
		SessionTurns: connectorRuntime,
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptInConversation(t, connection, sessionID, map[string]any{"conversationType": "channel", "addressing": map[string]any{"botMentioned": true}})

	if !decider.hasDecided {
		t.Fatal("the mention never reached the engagement decision")
	}
	if !decider.wasShowingAtDecision {
		t.Fatal("the mention waited on the engagement decision without the conversation being shown that the agent is working")
	}
	if adapter.isShowing() {
		t.Fatal("progress is still shown after the prompt returned")
	}
}

func promptInConversation(t *testing.T, connection *acp.ClientSideConnection, sessionID acp.SessionId, visibleContext map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errorValue := connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("이번 주 일정 정리해줘")},
		Meta: map[string]any{MessageMetaKey: map[string]any{
			"messageID":     "message-1",
			"replyTargetID": "reply-target-1",
			"context":       visibleContext,
		}},
	})
	if errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
}
