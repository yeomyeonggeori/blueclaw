package connectors

import "strings"

type MessagePlacement struct {
	ConversationID string
	ReplyTargetID  string
	IsThread       bool
}

type PostedQuestion struct {
	ConversationID string
	ReplyTargetID  string
	MessageID      string
}

func placementOf(event PlatformInboundEvent) MessagePlacement {
	return MessagePlacement{
		ConversationID: event.ConversationID,
		ReplyTargetID:  event.ReplyTargetID,
		IsThread:       eventIsThreadReply(event),
	}
}

func isReplyInThread(conversationID string, threadReplyTargetID string, reply MessagePlacement) bool {
	return reply.IsThread && reply.ConversationID == conversationID && reply.ReplyTargetID == threadReplyTargetID
}

func (question PostedQuestion) IsAnsweredBy(reply MessagePlacement) bool {
	if question.isInThread() {
		return isReplyInThread(question.ConversationID, question.ReplyTargetID, reply)
	}
	if !reply.IsThread {
		return reply.ConversationID == question.ConversationID
	}
	return isReplyInThread(question.ConversationID, question.threadRootedAtQuestion(), reply)
}

func (question PostedQuestion) isInThread() bool {
	replyTargetID := strings.TrimSpace(question.ReplyTargetID)
	return replyTargetID != "" && replyTargetID != question.ConversationID
}

func (question PostedQuestion) threadRootedAtQuestion() string {
	return question.ConversationID + ":" + question.MessageID
}
