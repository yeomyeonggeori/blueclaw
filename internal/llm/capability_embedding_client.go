package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

const DefaultEmbeddingModelName = "baai/bge-m3"

const (
	EmbeddingInputTypeQuery    = "query"
	EmbeddingInputTypeDocument = "document"
)

type CapabilityEmbeddingClient struct {
	CapabilityClient capability.Client
	ModelName        string
	ExecutionMode    string
	OutputDimensions int
}

type EmbeddingInput struct {
	Text      string
	InputType string
}

type capabilityEmbeddingRequestDocument struct {
	Input            any    `json:"input"`
	Model            string `json:"model,omitempty"`
	ExecutionMode    string `json:"executionMode,omitempty"`
	InputType        string `json:"inputType,omitempty"`
	OutputDimensions int    `json:"outputDimensions,omitempty"`
}

type capabilityEmbeddingResponseDocument struct {
	Provider        string      `json:"provider"`
	Model           string      `json:"model"`
	SelectedBackend string      `json:"selectedBackend"`
	Embedding       []float64   `json:"embedding"`
	Embeddings      [][]float64 `json:"embeddings"`
}

func (client CapabilityEmbeddingClient) GenerateEmbedding(ctx context.Context, input string) ([]float32, error) {
	return client.GenerateEmbeddingForInput(ctx, EmbeddingInput{Text: input})
}

func (client CapabilityEmbeddingClient) GenerateEmbeddingForInput(ctx context.Context, input EmbeddingInput) ([]float32, error) {
	responseDocument, errorValue := client.post(ctx, input.Text, input.InputType)
	if errorValue != nil {
		return nil, errorValue
	}
	if len(responseDocument.Embedding) == 0 {
		return nil, errors.New("capability embedding response carried no embedding")
	}
	return float32Embedding(responseDocument.Embedding), nil
}

func (client CapabilityEmbeddingClient) GenerateEmbeddings(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	responseDocument, errorValue := client.post(ctx, texts, inputType)
	if errorValue != nil {
		return nil, errorValue
	}
	if len(responseDocument.Embeddings) != len(texts) {
		return nil, fmt.Errorf("capability embedding response carried %d embeddings for %d inputs", len(responseDocument.Embeddings), len(texts))
	}
	embeddings := make([][]float32, 0, len(texts))
	for _, embedding := range responseDocument.Embeddings {
		embeddings = append(embeddings, float32Embedding(embedding))
	}
	return embeddings, nil
}

func (client CapabilityEmbeddingClient) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return client.GenerateEmbeddingForInput(ctx, EmbeddingInput{Text: text, InputType: EmbeddingInputTypeQuery})
}

func (client CapabilityEmbeddingClient) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	return client.GenerateEmbeddings(ctx, texts, EmbeddingInputTypeDocument)
}

func (client CapabilityEmbeddingClient) post(ctx context.Context, input any, inputType string) (capabilityEmbeddingResponseDocument, error) {
	if client.CapabilityClient.HTTPClient == nil {
		return capabilityEmbeddingResponseDocument{}, errors.New("capability embedding http client is not configured")
	}
	var responseDocument capabilityEmbeddingResponseDocument
	errorValue := client.CapabilityClient.PostJSON(
		ctx,
		"/v1/embedding/create",
		capabilityEmbeddingRequestDocument{
			Input:            input,
			Model:            client.ModelName,
			ExecutionMode:    firstNonEmptyEmbeddingString(client.ExecutionMode, "auto"),
			InputType:        inputType,
			OutputDimensions: client.OutputDimensions,
		},
		&responseDocument,
	)
	return responseDocument, errorValue
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
