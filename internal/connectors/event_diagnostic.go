package connectors

import (
	"encoding/json"
	"time"
)

type EventDiagnosticFilter struct {
	Platform       string
	ConversationID string
	MessageID      string
	Limit          int
}

type EventDiagnostic struct {
	RawEventID        string          `json:"rawEventID"`
	Platform          string          `json:"platform"`
	ConversationID    string          `json:"conversationID"`
	ExternalMessageID string          `json:"externalMessageID"`
	ConnectorStatus   string          `json:"connectorStatus"`
	AttemptCount      int             `json:"attemptCount"`
	ConnectorError    string          `json:"connectorError,omitempty"`
	Result            json.RawMessage `json:"result,omitempty"`
	IngestedAt        time.Time       `json:"ingestedAt"`
	StartedAt         *time.Time      `json:"startedAt,omitempty"`
	CompletedAt       *time.Time      `json:"completedAt,omitempty"`
	SenderName        string          `json:"senderName,omitempty"`
	PromptPreview     string          `json:"promptPreview,omitempty"`
	DecisionCallID    string          `json:"decisionCallID,omitempty"`
	Decision          json.RawMessage `json:"decision,omitempty"`
}
