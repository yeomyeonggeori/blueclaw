package approvalgate

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

type ConversationPoster interface {
	QuestionPoster
	Serves(platform string, conversationID string) bool
}

type routedQuestionPoster struct {
	primary   ConversationPoster
	otherwise QuestionPoster
}

func PosterRoutedBy(primary ConversationPoster, otherwise QuestionPoster) QuestionPoster {
	if otherwise == nil {
		return primary
	}
	return routedQuestionPoster{primary: primary, otherwise: otherwise}
}

func (poster routedQuestionPoster) PostQuestion(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question ParkedQuestion) error {
	if poster.primary.Serves(approvalRequest.Platform, approvalRequest.ConversationID) {
		return poster.primary.PostQuestion(ctx, approvalRequest, question)
	}
	return poster.otherwise.PostQuestion(ctx, approvalRequest, question)
}
