package connectors

import (
	"encoding/json"
	"os"
	"testing"
)

type threadReplyTargetCase struct {
	Name                string `json:"name"`
	RootMessageID       string `json:"rootMessageId"`
	ConversationID      string `json:"conversationId"`
	ThreadReplyTargetID string `json:"threadReplyTargetId"`
}

func TestAThreadRootedAtAQuestionIsAddressedTheWayChatdAddressesIt(t *testing.T) {
	document, errorValue := os.ReadFile("../../protocol/fixtures/thread-reply-targets.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	cases := []threadReplyTargetCase{}
	if errorValue := json.Unmarshal(document, &cases); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(cases) == 0 {
		t.Fatal("the fixture holds no cases")
	}
	for _, targetCase := range cases {
		t.Run(targetCase.Name, func(t *testing.T) {
			question := PostedQuestion{ConversationID: targetCase.ConversationID, MessageID: targetCase.RootMessageID}
			if actual := question.threadRootedAtQuestion(); actual != targetCase.ThreadReplyTargetID {
				t.Fatalf("expected the thread reply target %q, got %q", targetCase.ThreadReplyTargetID, actual)
			}
		})
	}
}
