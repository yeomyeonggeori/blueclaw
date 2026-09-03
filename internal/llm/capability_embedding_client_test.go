package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
)

func TestCapabilityEmbeddingClientSendsInputTypeAndDimensions(t *testing.T) {
	var receivedRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/embedding/create" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		if errorValue := json.NewDecoder(request.Body).Decode(&receivedRequest); errorValue != nil {
			t.Fatal(errorValue)
		}
		_ = json.NewEncoder(responseWriter).Encode(map[string]any{"embedding": []float64{0.25, 0.5}})
	}))
	defer server.Close()

	client := CapabilityEmbeddingClient{
		CapabilityClient: capability.Client{Endpoint: server.URL, HTTPClient: server.Client()},
		ModelName:        "baai/bge-m3",
		ExecutionMode:    "remote",
		OutputDimensions: 1024,
	}
	embedding, errorValue := client.EmbedQuery(context.Background(), "where is the Q3 review")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(embedding) != 2 || embedding[0] != 0.25 {
		t.Fatalf("expected the embedding to be decoded, got %v", embedding)
	}
	if receivedRequest["input"] != "where is the Q3 review" || receivedRequest["inputType"] != EmbeddingInputTypeQuery {
		t.Fatalf("expected a query input, got %v", receivedRequest)
	}
	if receivedRequest["outputDimensions"] != float64(1024) || receivedRequest["executionMode"] != "remote" || receivedRequest["model"] != "baai/bge-m3" {
		t.Fatalf("expected dimensions, mode and model to be sent, got %v", receivedRequest)
	}
}

func TestCapabilityEmbeddingClientBatchesDocuments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var receivedRequest map[string]any
		_ = json.NewDecoder(request.Body).Decode(&receivedRequest)
		inputs, _ := receivedRequest["input"].([]any)
		if len(inputs) != 2 || receivedRequest["inputType"] != EmbeddingInputTypeDocument {
			t.Fatalf("expected two document inputs, got %v", receivedRequest)
		}
		_ = json.NewEncoder(responseWriter).Encode(map[string]any{"embeddings": [][]float64{{1}, {2}}})
	}))
	defer server.Close()

	client := CapabilityEmbeddingClient{CapabilityClient: capability.Client{Endpoint: server.URL, HTTPClient: server.Client()}}
	embeddings, errorValue := client.EmbedDocuments(context.Background(), []string{"first", "second"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(embeddings) != 2 || embeddings[1][0] != 2 {
		t.Fatalf("expected two embeddings in order, got %v", embeddings)
	}
}

func TestCapabilityEmbeddingClientRejectsAShortBatchResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(responseWriter).Encode(map[string]any{"embeddings": [][]float64{{1}}})
	}))
	defer server.Close()

	client := CapabilityEmbeddingClient{CapabilityClient: capability.Client{Endpoint: server.URL, HTTPClient: server.Client()}}
	if _, errorValue := client.EmbedDocuments(context.Background(), []string{"first", "second"}); errorValue == nil {
		t.Fatal("expected a batch response with a missing embedding to be rejected")
	}
}
