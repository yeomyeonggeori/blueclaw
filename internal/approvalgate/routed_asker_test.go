package approvalgate

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type namedAsker struct {
	name               string
	servedConversation string
	asked              *[]string
}

func (asker namedAsker) Serves(_ string, conversationID string) bool {
	return conversationID == asker.servedConversation
}

func (asker namedAsker) AskPermission(context.Context, mcpserver.ApprovalRequest, PermissionQuestion) (ApprovalAnswer, AskStatus) {
	*asker.asked = append(*asker.asked, asker.name)
	return ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove}, AskAnswered
}

func (asker namedAsker) AskHarnessPermission(context.Context, mcpserver.ApprovalRequest, HarnessPermissionQuestion) (acp.RequestPermissionOutcome, AskStatus) {
	*asker.asked = append(*asker.asked, asker.name)
	return acp.RequestPermissionOutcome{}, AskAnswered
}

type plainAsker struct {
	asked *[]string
}

func (asker plainAsker) AskPermission(context.Context, mcpserver.ApprovalRequest, PermissionQuestion) (ApprovalAnswer, AskStatus) {
	*asker.asked = append(*asker.asked, "plain")
	return ApprovalAnswer{}, AskAnswered
}

func TestAConversationTheRelayServesIsAskedThroughTheRelayAndAnyOtherInTheThread(t *testing.T) {
	asked := []string{}
	routed := AskerRoutedBy(namedAsker{name: "relay", servedConversation: "acp-conversation", asked: &asked}, plainAsker{asked: &asked})

	routed.AskPermission(context.Background(), mcpserver.ApprovalRequest{ConversationID: "acp-conversation"}, PermissionQuestion{})
	routed.AskPermission(context.Background(), mcpserver.ApprovalRequest{ConversationID: "chat-conversation"}, PermissionQuestion{})

	if len(asked) != 2 || asked[0] != "relay" || asked[1] != "plain" {
		t.Fatalf("the two conversations were asked through %v", asked)
	}
}

func TestAHarnessQuestionInAConversationTheThreadAskerCannotCarryIsLeftUnanswered(t *testing.T) {
	asked := []string{}
	routed := AskerRoutedBy(namedAsker{name: "relay", servedConversation: "acp-conversation", asked: &asked}, plainAsker{asked: &asked})

	_, status := routed.(HarnessPermissionAsker).AskHarnessPermission(context.Background(), mcpserver.ApprovalRequest{ConversationID: "chat-conversation"}, HarnessPermissionQuestion{})

	if status == AskAnswered || len(asked) != 0 {
		t.Fatalf("a harness question reached %v and was answered=%v", asked, (status == AskAnswered))
	}
}

func TestWithoutAnotherAskerTheRelayAnswersEveryConversation(t *testing.T) {
	asked := []string{}
	relay := namedAsker{name: "relay", servedConversation: "acp-conversation", asked: &asked}

	if AskerRoutedBy(relay, nil) != PermissionAsker(relay) {
		t.Fatal("the relay was wrapped although nothing else could ask")
	}
}
