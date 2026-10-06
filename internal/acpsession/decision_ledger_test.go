package acpsession

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type judgedAddressedToSomebodyElse struct{ addressedToSomebodyElse }

func (judged judgedAddressedToSomebodyElse) Decide(ctx context.Context, facts inboundengagement.Facts, observe agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	observe(agentcontract.LLMCallRecord{Kind: agentcontract.LLMCallKindDecision, Model: "decision-model"})
	return judged.addressedToSomebodyElse.Decide(ctx, facts, observe)
}

type recordedTasklessCall struct {
	subjects []string
	record   agentcontract.LLMCallRecord
}

func TestADecisionNoTaskFollowedIsRecordedAgainstItsMessage(t *testing.T) {
	var mutex sync.Mutex
	recorded := []recordedTasklessCall{}
	launcher := &recordingLauncher{}
	connectorRuntime := connectorRuntimeDeciding(nil, judgedAddressedToSomebodyElse{})
	connectorRuntime.UseTasklessLLMCallRecorder(func(subjects []string, record agentcontract.LLMCallRecord) {
		mutex.Lock()
		defer mutex.Unlock()
		recorded = append(recorded, recordedTasklessCall{subjects: subjects, record: record})
	})
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher:       launcher,
		Directory:          staticDirectory{},
		ReplyReader:        scriptedReader{},
		SessionTurns:       connectorRuntime,
		AttachmentImporter: &recordingAttachmentImporter{},
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "channel")

	mutex.Lock()
	defer mutex.Unlock()
	if len(recorded) != 1 || recorded[0].record.Model != "decision-model" || !slices.Equal(recorded[0].subjects, []string{"message-picture"}) {
		t.Fatalf("expected the consumed message's decision recorded once against the message, got %+v", recorded)
	}
}
