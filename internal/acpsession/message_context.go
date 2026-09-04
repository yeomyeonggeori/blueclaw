package acpsession

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

const MessageMetaKey = "kim.intern/message"

type MessageContext struct {
	MessageID     string                    `json:"messageID,omitempty"`
	ReplyTargetID string                    `json:"replyTargetID,omitempty"`
	IsThread      bool                      `json:"isThread,omitempty"`
	Context       connectors.VisibleContext `json:"context"`
}

func MessageContextFromMeta(meta map[string]any) MessageContext {
	carried, isCarried := meta[MessageMetaKey]
	if !isCarried {
		return MessageContext{}
	}
	document, errorValue := json.Marshal(carried)
	if errorValue != nil {
		return MessageContext{}
	}
	messageContext := MessageContext{}
	if errorValue := json.Unmarshal(document, &messageContext); errorValue != nil {
		return MessageContext{}
	}
	messageContext.MessageID = strings.TrimSpace(messageContext.MessageID)
	messageContext.ReplyTargetID = strings.TrimSpace(messageContext.ReplyTargetID)
	return messageContext
}

func (messageContext MessageContext) conversationType(addressing Addressing) string {
	if conversationType := strings.TrimSpace(messageContext.Context.ConversationType); conversationType != "" {
		return conversationType
	}
	return addressing.ConversationType
}

func (messageContext MessageContext) responseLanguage(addressing Addressing) string {
	if responseLanguage := strings.TrimSpace(messageContext.Context.ResponseLanguage); responseLanguage != "" {
		return responseLanguage
	}
	return addressing.ResponseLanguage
}

func (messageContext MessageContext) replyTargetID(addressing Addressing) string {
	if messageContext.ReplyTargetID != "" {
		return messageContext.ReplyTargetID
	}
	return addressing.ReplyTargetID
}
