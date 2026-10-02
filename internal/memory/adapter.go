package memory

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluememo"
)

const RememberContentRuneLimit = 600

type ContainedCircleResolver interface {
	ContainedCircles() map[string][]string
}

type LanguageModel struct {
	Provider model.LanguageModelProvider
}

func (languageModel LanguageModel) GenerateStructured(ctx context.Context, request bluememo.StructuredRequest) (string, error) {
	response, errorValue := languageModel.Provider.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: request.Instruction},
			{Role: "user", Content: request.Subject},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               request.SchemaName,
			Document:           request.SchemaDocument,
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return response.Content, nil
}

func RememberContentGateMessage(content string) string {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return "memory_remember content is required"
	}
	if len([]rune(trimmedContent)) > RememberContentRuneLimit {
		return "memory_remember content must be a single compact fact"
	}
	return ""
}
