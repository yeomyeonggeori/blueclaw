package llm

import (
	"context"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

type CapabilityEmbeddingClient struct {
	CapabilityClient capability.Client
	ModelName        string
	ExecutionMode    string
}

type capabilityEmbeddingRequestDocument struct {
	Input         string `json:"input"`
	Model         string `json:"model,omitempty"`
	ExecutionMode string `json:"executionMode,omitempty"`
}

type capabilityEmbeddingResponseDocument struct {
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	SelectedBackend string    `json:"selectedBackend"`
	Embedding       []float64 `json:"embedding"`
}

func (client CapabilityEmbeddingClient) GenerateEmbedding(ctx context.Context, input string) ([]float32, error) {
	if client.CapabilityClient.HTTPClient == nil {
		return nil, errors.New("capability embedding http client is not configured")
	}
	var responseDocument capabilityEmbeddingResponseDocument
	errorValue := client.CapabilityClient.PostJSON(
		ctx,
		"/v1/embedding/create",
		capabilityEmbeddingRequestDocument{
			Input:         input,
			Model:         client.ModelName,
			ExecutionMode: firstNonEmptyEmbeddingString(client.ExecutionMode, "auto"),
		},
		&responseDocument,
	)
	if errorValue != nil {
		return nil, errorValue
	}
	return float32Embedding(responseDocument.Embedding), nil
}

func float32Embedding(values []float64) []float32 {
	embedding := make([]float32, 0, len(values))
	for _, value := range values {
		embedding = append(embedding, float32(value))
	}
	return embedding
}

func firstNonEmptyEmbeddingString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
