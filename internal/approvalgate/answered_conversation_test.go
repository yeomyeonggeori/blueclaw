package approvalgate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type answeredConversationCase struct {
	Name                           string          `json:"name"`
	ConversationType               string          `json:"conversationType"`
	ChannelID                      string          `json:"channelID"`
	Input                          json.RawMessage `json:"input"`
	LandsInTheAnsweredConversation bool            `json:"landsInTheAnsweredConversation"`
}

func TestAMessageIntoTheConversationBeingAnsweredRunsWithoutAsking(t *testing.T) {
	var unanswerableGate *Gate
	messageSend := toolcontract.ToolDefinition{Name: "message_send", RequiresApproval: true, SideEffectClass: toolcontract.ToolSideEffectExternalSend}
	for _, answeredCase := range answeredConversationCases(t) {
		t.Run(answeredCase.Name, func(t *testing.T) {
			turnGate := unanswerableGate.TurnGate(TurnContext{
				RequesterPersonID: "person-1",
				ConversationID:    "conversation-1",
				ConversationType:  answeredCase.ConversationType,
				ChannelID:         answeredCase.ChannelID,
			})
			review, errorValue := turnGate.ReviewToolCall(context.Background(), toolcontract.ToolInvocation{ToolName: "message_send", Input: answeredCase.Input}, messageSend)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if review.MayProceed != answeredCase.LandsInTheAnsweredConversation {
				t.Fatalf("expected MayProceed=%v without asking, got %+v", answeredCase.LandsInTheAnsweredConversation, review)
			}
		})
	}
}

func answeredConversationCases(t *testing.T) []answeredConversationCase {
	t.Helper()
	document, errorValue := os.ReadFile(filepath.Join("testdata", "answered_conversation_cases.json"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var cases []answeredConversationCase
	if errorValue := json.Unmarshal(document, &cases); errorValue != nil {
		t.Fatal(errorValue)
	}
	return cases
}
