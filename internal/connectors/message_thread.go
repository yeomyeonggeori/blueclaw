package connectors

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

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

func PostedApprovalQuestionMessageID(taskEvents []task.TaskEvent) string {
	messageID := ""
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHoldOpened:
			messageID = ""
		case agentcontract.TaskEventConnectorReplySent:
			messageID = firstNonEmptyString(approvalQuestionDispatchID(taskEvent.Body), messageID)
		}
	}
	return messageID
}

func approvalQuestionDispatchID(body string) string {
	sent := struct {
		ReplyKind  string `json:"replyKind"`
		DispatchID string `json:"dispatchID"`
	}{}
	if json.Unmarshal([]byte(body), &sent) != nil || sent.ReplyKind != connectorReplyKindApprovalQuestion {
		return ""
	}
	return strings.TrimSpace(sent.DispatchID)
}
