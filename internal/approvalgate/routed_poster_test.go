//go:build !nobundledharness

package approvalgate

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

type namedPoster struct {
	name               string
	servedConversation string
	posted             *[]string
}

func (poster namedPoster) Serves(_ string, conversationID string) bool {
	return conversationID == poster.servedConversation
}

func (poster namedPoster) PostQuestion(context.Context, mcpserver.ApprovalRequest, ParkedQuestion) error {
	*poster.posted = append(*poster.posted, poster.name)
	return nil
}

type plainPoster struct {
	posted *[]string
}

func (poster plainPoster) PostQuestion(context.Context, mcpserver.ApprovalRequest, ParkedQuestion) error {
	*poster.posted = append(*poster.posted, "plain")
	return nil
}

func TestAQuestionGoesToTheSessionThatServesItsConversationAndOtherwiseToTheThread(t *testing.T) {
	posted := []string{}
	routed := PosterRoutedBy(namedPoster{name: "session", servedConversation: "acp-conversation", posted: &posted}, plainPoster{posted: &posted})

	routed.PostQuestion(context.Background(), mcpserver.ApprovalRequest{ConversationID: "acp-conversation"}, ParkedQuestion{})
	routed.PostQuestion(context.Background(), mcpserver.ApprovalRequest{ConversationID: "chat-conversation"}, ParkedQuestion{})

	if len(posted) != 2 || posted[0] != "session" || posted[1] != "plain" {
		t.Fatalf("the questions were posted by %v, expected the session then the thread", posted)
	}
}

func TestWithoutAnotherPosterTheSessionPostsEveryQuestion(t *testing.T) {
	posted := []string{}
	session := namedPoster{name: "session", servedConversation: "acp-conversation", posted: &posted}

	if PosterRoutedBy(session, nil) != QuestionPoster(session) {
		t.Fatal("with no thread poster the session is the only poster")
	}
}
