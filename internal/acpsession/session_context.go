package acpsession

import (
	"encoding/json"
	"strings"
)

const SessionMetaKey = "kim.intern/session"

type Requester struct {
	Email       string `json:"email"`
	PersonID    string `json:"personID,omitempty"`
	Name        string `json:"name,omitempty"`
	CallingName string `json:"callingName,omitempty"`
	Handle      string `json:"handle,omitempty"`
}

type Addressing struct {
	Platform         string `json:"platform"`
	ConversationID   string `json:"conversationID"`
	ConversationType string `json:"conversationType,omitempty"`
	ReplyTargetID    string `json:"replyTargetID,omitempty"`
	IsThread         bool   `json:"isThread,omitempty"`
	ResponseLanguage string `json:"responseLanguage,omitempty"`
}

type SessionContext struct {
	Requester  Requester  `json:"requester"`
	Addressing Addressing `json:"addressing"`
}

func SessionContextFromMeta(meta map[string]any) (SessionContext, error) {
	carried, isCarried := meta[SessionMetaKey]
	if !isCarried {
		return SessionContext{}, errSessionNamesNobody
	}
	document, errorValue := json.Marshal(carried)
	if errorValue != nil {
		return SessionContext{}, errorValue
	}
	sessionContext := SessionContext{}
	if errorValue := json.Unmarshal(document, &sessionContext); errorValue != nil {
		return SessionContext{}, errorValue
	}
	sessionContext.Requester.Email = strings.ToLower(strings.TrimSpace(sessionContext.Requester.Email))
	sessionContext.Requester.PersonID = strings.TrimSpace(sessionContext.Requester.PersonID)
	sessionContext.Addressing.Platform = strings.TrimSpace(sessionContext.Addressing.Platform)
	sessionContext.Addressing.ConversationID = strings.TrimSpace(sessionContext.Addressing.ConversationID)
	if sessionContext.Requester.Email == "" && sessionContext.Requester.PersonID == "" {
		return SessionContext{}, errSessionNamesNobody
	}
	if sessionContext.Addressing.ConversationID == "" {
		return SessionContext{}, errSessionNamesNoConversation
	}
	return sessionContext, nil
}

func conversationKey(platform string, conversationID string) string {
	return strings.TrimSpace(platform) + "\x00" + strings.TrimSpace(conversationID)
}
