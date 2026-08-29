package agentruntime

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

var conversationHistoryInputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"historyCursor":{"type":"string","pattern":"\\S"},
		"limit":{"type":"integer","minimum":1,"maximum":50}
	},
	"additionalProperties":false
}`)

var conversationHistoryResultSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"messages":{
			"type":"array",
			"items":{
				"type":"object",
				"properties":{
					"speaker":{"type":"string"},
					"speakerCallingName":{"type":"string"},
					"speakerHandle":{"type":"string"},
					"text":{"type":"string"},
					"sentAt":{"type":"string","format":"date-time"},
					"materials":{
						"type":"array",
						"items":{
							"type":"object",
							"properties":{
								"url":{"type":"string"},
								"filename":{"type":"string"},
								"contentType":{"type":"string"},
								"sizeBytes":{"type":"integer","minimum":0},
								"isAvailable":{"type":"boolean"},
								"errorCode":{"type":"string"},
								"message":{"type":"string"},
								"markdownPreview":{"type":"string"},
								"conversionStatus":{"type":"string"},
								"conversionMessage":{"type":"string"}
							},
							"required":["sizeBytes","isAvailable"],
							"additionalProperties":false
						}
					}
				},
				"required":["speaker","text","materials"],
				"additionalProperties":false
			}
		},
		"hasMoreBefore":{"type":"boolean"},
		"historyCursor":{"type":"string"}
	},
	"required":["messages","hasMoreBefore","historyCursor"],
	"additionalProperties":false
}`)

type conversationHistoryToolOutput struct {
	Messages      []conversationHistoryMessage `json:"messages"`
	HasMoreBefore bool                         `json:"hasMoreBefore"`
	HistoryCursor string                       `json:"historyCursor"`
}

type conversationHistoryMessage struct {
	Speaker            string                        `json:"speaker"`
	SpeakerCallingName string                        `json:"speakerCallingName,omitempty"`
	SpeakerHandle      string                        `json:"speakerHandle,omitempty"`
	Text               string                        `json:"text"`
	SentAt             string                        `json:"sentAt,omitempty"`
	Materials          []conversationHistoryMaterial `json:"materials"`
}

type conversationHistoryMaterial struct {
	URL               string `json:"url,omitempty"`
	Filename          string `json:"filename,omitempty"`
	ContentType       string `json:"contentType,omitempty"`
	SizeBytes         int64  `json:"sizeBytes"`
	IsAvailable       bool   `json:"isAvailable"`
	ErrorCode         string `json:"errorCode,omitempty"`
	Message           string `json:"message,omitempty"`
	MarkdownPreview   string `json:"markdownPreview,omitempty"`
	ConversionStatus  string `json:"conversionStatus,omitempty"`
	ConversionMessage string `json:"conversionMessage,omitempty"`
}

func projectConversationHistory(visibleContext agentcontract.VisibleContext) conversationHistoryToolOutput {
	messages := make([]conversationHistoryMessage, 0, len(visibleContext.Messages))
	for _, message := range visibleContext.Messages {
		messages = append(messages, projectConversationHistoryMessage(message))
	}
	return conversationHistoryToolOutput{
		Messages:      messages,
		HasMoreBefore: visibleContext.HasMoreBefore,
		HistoryCursor: strings.TrimSpace(visibleContext.HistoryCursor),
	}
}

func projectConversationHistoryMessage(message agentcontract.VisibleContextMessage) conversationHistoryMessage {
	materials := make([]conversationHistoryMaterial, 0, len(message.Materials))
	for _, material := range message.Materials {
		materials = append(materials, projectConversationHistoryMaterial(material))
	}
	return conversationHistoryMessage{
		Speaker:            message.Speaker,
		SpeakerCallingName: message.SpeakerCallingName,
		SpeakerHandle:      message.SpeakerHandle,
		Text:               message.Text,
		SentAt:             formatConversationHistoryTime(message.SentAt),
		Materials:          materials,
	}
}

func projectConversationHistoryMaterial(material agentcontract.VisibleContextMaterial) conversationHistoryMaterial {
	return conversationHistoryMaterial{
		URL:               material.URL,
		Filename:          material.Filename,
		ContentType:       material.ContentType,
		SizeBytes:         max(0, material.SizeBytes),
		IsAvailable:       material.IsAvailable,
		ErrorCode:         material.ErrorCode,
		Message:           material.Message,
		MarkdownPreview:   material.MarkdownPreview,
		ConversionStatus:  material.ConversionStatus,
		ConversionMessage: material.ConversionMessage,
	}
}

func formatConversationHistoryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}
