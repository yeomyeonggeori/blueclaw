package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type OpenAIEmbeddingClient struct {
	Endpoint   string
	APIKey     string
	ModelName  string
	Dimensions int
	HTTPClient *http.Client
}

type openAIEmbeddingRequest struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func (client OpenAIEmbeddingClient) GenerateEmbedding(ctx context.Context, input string) ([]float32, error) {
	requestDocument, errorValue := json.Marshal(openAIEmbeddingRequest{Model: client.ModelName, Input: input, Dimensions: client.Dimensions})
	if errorValue != nil {
		return nil, errorValue
	}
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(client.Endpoint), bytes.NewReader(requestDocument))
	if errorValue != nil {
		return nil, errorValue
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(client.APIKey); apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, errorValue := client.httpClient().Do(request)
	if errorValue != nil {
		return nil, errorValue
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.New("embedding endpoint returned " + response.Status)
	}
	var responseDocument openAIEmbeddingResponse
	if errorValue := json.NewDecoder(response.Body).Decode(&responseDocument); errorValue != nil {
		return nil, errorValue
	}
	if len(responseDocument.Data) == 0 || len(responseDocument.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding endpoint returned no vector")
	}
	return float32Embedding(responseDocument.Data[0].Embedding), nil
}

func (client OpenAIEmbeddingClient) httpClient() *http.Client {
	if client.HTTPClient != nil {
		return client.HTTPClient
	}
	return &http.Client{}
}
