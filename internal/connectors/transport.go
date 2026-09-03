package connectors

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type HTTPWebhookTransport struct {
	TransportName string
	PlatformName  string
}

func NewHTTPWebhookTransport(transportName string, platformName string) HTTPWebhookTransport {
	return HTTPWebhookTransport{
		TransportName: transportName,
		PlatformName:  platformName,
	}
}

func (transport HTTPWebhookTransport) Name() string {
	return transport.TransportName
}

func (transport HTTPWebhookTransport) Platform() string {
	return transport.PlatformName
}

func (transport HTTPWebhookTransport) Start(context.Context) {}

type capabilityIdentityRequest struct {
	SenderID string `json:"senderID"`
}

type capabilityProgressRequest struct {
	ReplyTargetID string `json:"replyTargetID"`
}

type capabilityReactionRequest struct {
	ConversationID string `json:"conversationID"`
	MessageID      string `json:"messageID"`
	EmojiName      string `json:"emojiName"`
	Reason         string `json:"reason"`
}

type capabilityReplyRequest struct {
	ReplyTargetID      string                        `json:"replyTargetID"`
	AnsweringMessageID string                        `json:"answeringMessageID,omitempty"`
	Message            string                        `json:"message"`
	TaskRunID          string                        `json:"taskRunID,omitempty"`
	ReplyKind          string                        `json:"replyKind,omitempty"`
	RawEventID         string                        `json:"rawEventID,omitempty"`
	OutboxID           string                        `json:"outboxID,omitempty"`
	Attachments        []capabilityReplyAttachment   `json:"attachments,omitempty"`
	RecoveryActions    []toolcontract.RecoveryAction `json:"recoveryActions,omitempty"`
	FailureNotice      agentcontract.FailureNotice   `json:"failureNotice,omitempty"`
	Interaction        *AskInteraction               `json:"interaction,omitempty"`
}

type capabilityReplyAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

type capabilityHistoryRequest struct {
	HistoryCursor string `json:"historyCursor"`
	Limit         int    `json:"limit"`
	Direction     string `json:"direction"`
}

type capabilityReplyResponse struct {
	DispatchID string `json:"dispatchID"`
}

type capabilityMessageEditRequest struct {
	ReplyTargetID string `json:"replyTargetID"`
	MessageID     string `json:"messageID"`
	Message       string `json:"message"`
}

type capabilityMessageDeleteRequest struct {
	ReplyTargetID string `json:"replyTargetID"`
	MessageID     string `json:"messageID"`
}

type normalizedEventEnvelope struct {
	Event PlatformInboundEvent `json:"event"`
}

func buildCapabilityReplyAttachments(attachments []toolcontract.FileAttachment) []capabilityReplyAttachment {
	replyAttachments := []capabilityReplyAttachment{}
	for _, attachment := range attachments {
		replyAttachments = append(replyAttachments, capabilityReplyAttachment{
			DevicePath:    attachment.DevicePath,
			Filename:      attachment.Filename,
			ContentType:   attachment.ContentType,
			SizeBytes:     attachment.SizeBytes,
			Title:         attachment.Title,
			ContentBase64: attachment.ContentBase64,
		})
	}
	return replyAttachments
}

func ParseNormalizedInboundEvent(payload []byte, platform string, source string) (PlatformInboundEvent, bool, error) {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return PlatformInboundEvent{}, false, nil
	}

	var event PlatformInboundEvent
	errorValue := json.Unmarshal(payload, &event)
	if errorValue != nil {
		return PlatformInboundEvent{}, false, errorValue
	}

	if strings.TrimSpace(event.MessageID) == "" {
		var envelope normalizedEventEnvelope
		errorValue = json.Unmarshal(payload, &envelope)
		if errorValue != nil {
			return PlatformInboundEvent{}, false, errorValue
		}
		event = envelope.Event
	}

	if strings.TrimSpace(event.MessageID) == "" {
		return PlatformInboundEvent{}, false, nil
	}

	event.Platform = platform
	if strings.TrimSpace(event.Source) == "" {
		event.Source = source
	}
	if event.RawReceivedAt.IsZero() {
		event.RawReceivedAt = time.Now()
	}

	return event, true, nil
}
