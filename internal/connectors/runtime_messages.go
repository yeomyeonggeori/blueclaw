package connectors

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type PlatformInboundEvent struct {
	Platform         string                    `json:"-"`
	Source           string                    `json:"-"`
	ConversationID   string                    `json:"conversationID"`
	MessageID        string                    `json:"messageID"`
	EventID          string                    `json:"eventID,omitempty"`
	SenderID         string                    `json:"senderID"`
	ReplyTargetID    string                    `json:"replyTargetID"`
	IsThread         *bool                     `json:"isThread,omitempty"`
	PreviousMessages []PendingRequestMessage   `json:"previousMessages,omitempty"`
	Prompt           string                    `json:"prompt"`
	InputParts       []agentcontract.AgentPart `json:"inputParts,omitempty"`
	ResponseLanguage string                    `json:"responseLanguage,omitempty"`
	Context          VisibleContext            `json:"context"`
	RawReceivedAt    time.Time                 `json:"-"`
	LegacyFields     map[string]interface{}    `json:"legacyFields,omitempty"`
	TaskRetry        *TaskRetryReference       `json:"taskRetry,omitempty"`

	intakeDecision *inboundDecision
}

type TaskRetryReference struct {
	SourceTaskRunID string `json:"sourceTaskRunID"`
	TaskRunID       string `json:"taskRunID"`
}

type ReplyTarget struct {
	ConversationID string `json:"conversationID"`
	ReplyTargetID  string `json:"replyTargetID"`
	// AnsweringMessageID is the message this reply answers. The thread says where
	// the reply belongs; this says what it is a reply to, so somebody who wrote
	// deep in a thread does not find the answer at the top of it.
	AnsweringMessageID string `json:"answeringMessageID,omitempty"`
	DedupeKey          string `json:"dedupeKey"`
}

type ReactionTarget struct {
	Platform       string `json:"platform"`
	ConversationID string `json:"conversationID"`
	MessageID      string `json:"messageID"`
	EmojiName      string `json:"emojiName"`
	Reason         string `json:"reason"`
}

type OutboundReply struct {
	Message         string                        `json:"message"`
	TaskRunID       string                        `json:"taskRunID,omitempty"`
	ReplyKind       string                        `json:"replyKind,omitempty"`
	RawEventID      string                        `json:"rawEventID,omitempty"`
	OutboxID        string                        `json:"outboxID,omitempty"`
	Attachments     []toolcontract.FileAttachment `json:"attachments,omitempty"`
	RecoveryActions []toolcontract.RecoveryAction `json:"recoveryActions,omitempty"`
	FailureNotice   agentcontract.FailureNotice   `json:"failureNotice,omitempty"`
	Interaction     *AskInteraction               `json:"interaction,omitempty"`
}

type outboundReplyDocument struct {
	Message         string                        `json:"message"`
	TaskRunID       string                        `json:"taskRunID,omitempty"`
	ReplyKind       string                        `json:"replyKind,omitempty"`
	RawEventID      string                        `json:"rawEventID,omitempty"`
	OutboxID        string                        `json:"outboxID,omitempty"`
	Attachments     []outboundReplyAttachment     `json:"attachments,omitempty"`
	RecoveryActions []toolcontract.RecoveryAction `json:"recoveryActions,omitempty"`
	FailureNotice   agentcontract.FailureNotice   `json:"failureNotice,omitempty"`
	Interaction     *AskInteraction               `json:"interaction,omitempty"`
}

type outboundReplyAttachment struct {
	DevicePath    string `json:"devicePath"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	Title         string `json:"title,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

type AskInteraction struct {
	InteractionID        string            `json:"interactionID"`
	TaskRunID            string            `json:"taskRunID"`
	Kind                 string            `json:"kind"`
	Message              string            `json:"message,omitempty"`
	Question             string            `json:"question,omitempty"`
	Options              []AskChoiceOption `json:"options,omitempty"`
	RecommendedOptionKey string            `json:"recommendedOptionKey,omitempty"`
	SelectionMode        string            `json:"selectionMode,omitempty"`
	ResponseLanguage     string            `json:"responseLanguage,omitempty"`
	TargetPlatformUserID string            `json:"targetPlatformUserID,omitempty"`
}

type AskChoiceOption struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ShortLabel string `json:"shortLabel,omitempty"`
	Value      string `json:"value,omitempty"`
}

func (reply OutboundReply) MarshalJSON() ([]byte, error) {
	document := outboundReplyDocument{
		Message:         reply.Message,
		TaskRunID:       reply.TaskRunID,
		ReplyKind:       reply.ReplyKind,
		RawEventID:      reply.RawEventID,
		OutboxID:        reply.OutboxID,
		Attachments:     outboundReplyAttachments(reply.Attachments),
		RecoveryActions: reply.RecoveryActions,
		FailureNotice:   reply.FailureNotice,
		Interaction:     reply.Interaction,
	}
	return json.Marshal(document)
}

func (reply *OutboundReply) UnmarshalJSON(documentBytes []byte) error {
	var document outboundReplyDocument
	if errorValue := json.Unmarshal(documentBytes, &document); errorValue != nil {
		return errorValue
	}
	reply.Message = document.Message
	reply.TaskRunID = document.TaskRunID
	reply.ReplyKind = document.ReplyKind
	reply.RawEventID = document.RawEventID
	reply.OutboxID = document.OutboxID
	reply.Attachments = fileAttachmentsFromOutboundReplyAttachments(document.Attachments)
	reply.RecoveryActions = append([]toolcontract.RecoveryAction{}, document.RecoveryActions...)
	reply.FailureNotice = document.FailureNotice
	reply.Interaction = document.Interaction
	return nil
}

func outboundReplyAttachments(attachments []toolcontract.FileAttachment) []outboundReplyAttachment {
	replyAttachments := []outboundReplyAttachment{}
	for _, attachment := range attachments {
		replyAttachments = append(replyAttachments, outboundReplyAttachment{
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

func fileAttachmentsFromOutboundReplyAttachments(attachments []outboundReplyAttachment) []toolcontract.FileAttachment {
	fileAttachments := []toolcontract.FileAttachment{}
	for _, attachment := range attachments {
		fileAttachments = append(fileAttachments, toolcontract.FileAttachment{
			DevicePath:    attachment.DevicePath,
			Filename:      attachment.Filename,
			ContentType:   attachment.ContentType,
			SizeBytes:     attachment.SizeBytes,
			Title:         attachment.Title,
			ContentBase64: attachment.ContentBase64,
		})
	}
	return fileAttachments
}

type QueuedConnectorEvent struct {
	Event        PlatformInboundEvent
	AttemptCount int
}

type QueuedConnectorReply struct {
	OutboxID     string
	RawEventID   string
	Platform     string
	ReplyTarget  ReplyTarget
	Reply        OutboundReply
	AttemptCount int
}

type VisibleContext struct {
	Messages         []VisibleContextMessage `json:"messages"`
	HasMoreBefore    bool                    `json:"hasMoreBefore"`
	HistoryCursor    string                  `json:"historyCursor"`
	ResponseLanguage string                  `json:"responseLanguage,omitempty"`
	Sender           VisibleContextSender    `json:"sender,omitempty"`
	ConversationType string                  `json:"conversationType,omitempty"`
	ChannelID        string                  `json:"channelID,omitempty"`
	ChannelName      string                  `json:"channelName,omitempty"`
	Addressing       AddressingMetadata      `json:"addressing,omitempty"`
	AttachmentsOnly  bool                    `json:"attachmentsOnly,omitempty"`
	// MessagesOpenOtherExchanges says the messages are how other conversations in
	// the same place opened, rather than the conversation being continued.
	MessagesOpenOtherExchanges bool              `json:"messagesOpenOtherExchanges,omitempty"`
	InputAttachments           []InputAttachment `json:"inputAttachments,omitempty"`
	Materials                  []InputAttachment `json:"materials,omitempty"`
}

type InputAttachment struct {
	Platform    string `json:"platform,omitempty"`
	FileID      string `json:"fileID,omitempty"`
	URL         string `json:"url,omitempty"`
	MessageID   string `json:"messageID,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	Path        string `json:"path,omitempty"`
	// ContentBase64 carries the fetched file from the bridge that could reach the
	// platform to the workspace it belongs in. It lives only for that hop: the
	// file is written here and the field is cleared before anything records it.
	ContentBase64 string `json:"contentBase64,omitempty"`
	IsAvailable   bool   `json:"isAvailable,omitempty"`
	ErrorCode     string `json:"errorCode,omitempty"`
	Message       string `json:"message,omitempty"`
}

type AddressingMetadata struct {
	BotMentioned         bool `json:"botMentioned,omitempty"`
	OtherPersonMentioned bool `json:"otherPersonMentioned,omitempty"`
}

type VisibleContextSender struct {
	Platform    string `json:"platform,omitempty"`
	SenderID    string `json:"senderID,omitempty"`
	Handle      string `json:"handle,omitempty"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	CallingName string `json:"callingName,omitempty"`
}

type VisibleContextMessage struct {
	Speaker            string            `json:"speaker"`
	SpeakerCallingName string            `json:"speakerCallingName,omitempty"`
	SpeakerHandle      string            `json:"speakerHandle,omitempty"`
	Text               string            `json:"text"`
	SentAt             time.Time         `json:"sentAt,omitempty"`
	InputAttachments   []InputAttachment `json:"inputAttachments,omitempty"`
}

type HTTPParseResult struct {
	Event             PlatformInboundEvent
	HasEvent          bool
	ImmediateResponse *HTTPResponse
}

type HTTPResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

type ConnectorRuntimeResult struct {
	Handled         bool   `json:"handled"`
	Platform        string `json:"platform"`
	Duplicate       bool   `json:"duplicate"`
	Ignored         bool   `json:"ignored"`
	Reason          string `json:"reason,omitempty"`
	TaskRunID       string `json:"taskRunID,omitempty"`
	ReplyDispatchID string `json:"replyDispatchID,omitempty"`
}

func (event PlatformInboundEvent) DedupeKey() string {
	conversationID := strings.TrimSpace(event.ConversationID)
	return event.Platform + ":" + conversationID + ":" + event.ExternalEventID()
}

func (event PlatformInboundEvent) ExternalEventID() string {
	return firstNonEmptyString(strings.TrimSpace(event.EventID), strings.TrimSpace(event.MessageID))
}

func (event *PlatformInboundEvent) UnmarshalJSON(document []byte) error {
	type platformInboundEvent PlatformInboundEvent
	var parsedEvent platformInboundEvent
	if errorValue := json.Unmarshal(document, &parsedEvent); errorValue != nil {
		return errorValue
	}

	var rawFields map[string]interface{}
	if errorValue := json.Unmarshal(document, &rawFields); errorValue == nil {
		if len(parsedEvent.LegacyFields) == 0 {
			parsedEvent.LegacyFields = rawFields
		}
	}

	if strings.TrimSpace(parsedEvent.Prompt) == "" {
		parsedEvent.Prompt = stringField(rawFields, "text")
	}
	if strings.TrimSpace(parsedEvent.SenderID) == "" {
		parsedEvent.SenderID = stringField(rawFields, "senderUserID")
	}

	*event = PlatformInboundEvent(parsedEvent)
	return nil
}

func (visibleContext VisibleContext) ToAgentVisibleContext() agentcontract.VisibleContext {
	messages := make([]agentcontract.VisibleContextMessage, 0, len(visibleContext.Messages))
	for _, message := range visibleContext.Messages {
		messages = append(messages, agentcontract.VisibleContextMessage{
			Speaker:            message.Speaker,
			SpeakerCallingName: message.SpeakerCallingName,
			SpeakerHandle:      message.SpeakerHandle,
			Text:               message.Text,
			SentAt:             message.SentAt,
			Materials:          agentVisibleContextMaterials(message.InputAttachments),
		})
	}

	currentMaterials := agentVisibleContextMaterials(visibleContext.InputAttachments)
	return agentcontract.VisibleContext{
		Messages:                   messages,
		MessagesOpenOtherExchanges: visibleContext.MessagesOpenOtherExchanges,
		CurrentMaterials:           currentMaterials,
		Materials:                  agentPreviousVisibleContextMaterials(visibleContext.Materials, currentMaterials),
		HasMoreBefore:              visibleContext.HasMoreBefore,
		HistoryCursor:              visibleContext.HistoryCursor,
		ResponseLanguage:           visibleContext.ResponseLanguage,
	}
}

func agentPreviousVisibleContextMaterials(attachments []InputAttachment, currentMaterials []agentcontract.VisibleContextMaterial) []agentcontract.VisibleContextMaterial {
	currentMaterialIDs := map[string]bool{}
	for _, material := range currentMaterials {
		currentMaterialID := strings.TrimSpace(material.MaterialID)
		if currentMaterialID != "" {
			currentMaterialIDs[currentMaterialID] = true
		}
	}
	materials := []agentcontract.VisibleContextMaterial{}
	for _, material := range agentVisibleContextMaterials(attachments) {
		if currentMaterialIDs[strings.TrimSpace(material.MaterialID)] {
			continue
		}
		materials = append(materials, material)
	}
	return materials
}

// An attachment that failed to come in stays in the catalog with its error, or
// the model is left with nothing but the url in the message text and invents a
// path from it. Only an attachment with no identity at all is dropped.
func agentVisibleContextMaterials(attachments []InputAttachment) []agentcontract.VisibleContextMaterial {
	materials := make([]agentcontract.VisibleContextMaterial, 0, len(attachments))
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.FileID) == "" && strings.TrimSpace(attachment.Path) == "" &&
			strings.TrimSpace(attachment.URL) == "" {
			continue
		}
		materials = append(materials, agentcontract.VisibleContextMaterial{
			MaterialID:  attachmentMaterialID(attachment),
			URL:         strings.TrimSpace(attachment.URL),
			FileID:      strings.TrimSpace(attachment.FileID),
			Platform:    attachment.Platform,
			MessageID:   attachment.MessageID,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			SizeBytes:   attachment.SizeBytes,
			Path:        attachment.Path,
			IsAvailable: attachment.IsAvailable,
			ErrorCode:   attachment.ErrorCode,
			Message:     attachment.Message,
		})
	}
	return materials
}

func attachmentMaterialID(attachment InputAttachment) string {
	fileID := strings.TrimSpace(attachment.FileID)
	if fileID != "" {
		return firstNonEmptyString(strings.TrimSpace(attachment.Platform), "attachment") + ":" + fileID
	}
	return firstNonEmptyString(strings.TrimSpace(attachment.Platform), "attachment") + ":" + connectorSafePathSegment(firstNonEmptyString(attachment.Path, attachment.Filename, attachment.URL))
}

func responseLanguageForEvent(event PlatformInboundEvent) string {
	return toolcontract.ResolveResponseLanguage(event.ResponseLanguage, event.Context.ResponseLanguage)
}

func stringField(fields map[string]interface{}, name string) string {
	if fields == nil {
		return ""
	}
	value, isFound := fields[name]
	if !isFound {
		return ""
	}
	stringValue, isString := value.(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(stringValue)
}
