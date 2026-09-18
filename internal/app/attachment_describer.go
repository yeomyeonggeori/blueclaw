package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const attachmentDescriptionSystemPrompt = "Describe each picture the way somebody would describe it to a colleague who cannot see it: what it is, what is written on it, and what it appears to be for. One or two sentences each, in the order the pictures arrive. Describe only what is there."

const attachmentDescriptionSchema = `{"type":"object","properties":{"descriptions":{"type":"array","items":{"type":"string"}}},"required":["descriptions"],"additionalProperties":false}`

// languageModelAttachmentDescriber turns pictures into sentences for the
// decision model, which reads text and nothing else. It runs once, for a
// message whose whole content is an attachment, and only after the engagement
// gate has already let that message through.
type languageModelAttachmentDescriber struct {
	languageModel model.LanguageModelProvider
}

type attachmentDescriptionDocument struct {
	Descriptions []string `json:"descriptions"`
}

func newAttachmentDescriber(languageModel model.LanguageModelProvider) *languageModelAttachmentDescriber {
	if languageModel == nil {
		return nil
	}
	return &languageModelAttachmentDescriber{languageModel: languageModel}
}

func (describer *languageModelAttachmentDescriber) DescribeAttachments(ctx context.Context, parts []agentcontract.AgentPart) ([]string, error) {
	imageParts := agentcontract.ImageMessageParts(parts)
	if len(imageParts) == 0 {
		return nil, nil
	}
	response, errorValue := describer.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: attachmentDescriptionSystemPrompt},
			{Role: "user", Parts: imageParts},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               agentcontract.AttachmentDescriptionSchemaName,
			Document:           attachmentDescriptionSchema,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return nil, errorValue
	}
	var document attachmentDescriptionDocument
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(response.Content)), &document); errorValue != nil {
		return nil, errors.New("the attachment description was not readable: " + errorValue.Error())
	}
	return document.Descriptions, nil
}
