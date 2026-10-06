package acpsession

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type judgedAddressedToSomebodyElse struct{ addressedToSomebodyElse }

func (judged judgedAddressedToSomebodyElse) Decide(ctx context.Context, request agentcontract.IntakeDecisionRequest, callLedger *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	callLedger.Observe(agentcontract.LLMCallRecord{Kind: agentcontract.LLMCallKindDecision, Model: "decision-model"})
	return judged.addressedToSomebodyElse.Decide(ctx, request, callLedger)
}

type recordedTasklessCall struct {
	subjects []string
	record   agentcontract.LLMCallRecord
}

func TestADecisionNoTaskFollowedIsRecordedAgainstItsMessage(t *testing.T) {
	var mutex sync.Mutex
	recorded := []recordedTasklessCall{}
	launcher := &recordingLauncher{}
	connection, _ := connectedPairWithCollaborators(t, &recordingClient{}, Collaborators{
		TaskLauncher:       launcher,
		Directory:          staticDirectory{},
		ReplyReader:        scriptedReader{},
		IntakeDecider:      judgedAddressedToSomebodyElse{},
		AttachmentImporter: &recordingAttachmentImporter{},
		TasklessCalls: func(subjects []string, record agentcontract.LLMCallRecord) {
			mutex.Lock()
			defer mutex.Unlock()
			recorded = append(recorded, recordedTasklessCall{subjects: subjects, record: record})
		},
	})
	sessionID := openSessionForTest(t, connection, sessionMeta("sample@example.test", "conversation-1"))

	promptWithPicture(t, connection, sessionID, "channel")

	mutex.Lock()
	defer mutex.Unlock()
	if len(recorded) != 1 || recorded[0].record.Model != "decision-model" || !slices.Equal(recorded[0].subjects, []string{"message-picture"}) {
		t.Fatalf("expected the consumed message's decision recorded once against the message, got %+v", recorded)
	}
}
