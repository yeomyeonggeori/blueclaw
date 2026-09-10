package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

const openRouterBaseURL = "https://openrouter.ai/api/v1"

type openRouterEmbeddingClient struct {
	apiKey     string
	modelName  string
	dimensions int
}

func (client openRouterEmbeddingClient) GenerateEmbedding(ctx context.Context, input string) ([]float32, error) {
	body, errorValue := json.Marshal(map[string]any{"model": client.modelName, "input": input, "dimensions": client.dimensions})
	if errorValue != nil {
		return nil, errorValue
	}
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, openRouterBaseURL+"/embeddings", bytes.NewReader(body))
	if errorValue != nil {
		return nil, errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+client.apiKey)
	httpResponse, errorValue := http.DefaultClient.Do(httpRequest)
	if errorValue != nil {
		return nil, errorValue
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		return nil, errors.New("embedding endpoint returned " + httpResponse.Status)
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if errorValue := json.NewDecoder(httpResponse.Body).Decode(&decoded); errorValue != nil {
		return nil, errorValue
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding endpoint returned no vector")
	}
	return decoded.Data[0].Embedding, nil
}
