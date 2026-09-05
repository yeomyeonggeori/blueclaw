package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/llm"
)

func TestLowTierEscalatesToMediumThroughRealCapabilityTransport(t *testing.T) {
	requestedModels := []string{}
	var requestedModelsLock sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "/llm/structured") {
			http.Error(responseWriter, "unexpected path "+request.URL.Path, http.StatusNotFound)
			return
		}
		var document map[string]any
		if errorValue := json.NewDecoder(request.Body).Decode(&document); errorValue != nil {
			http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
			return
		}
		modelName, _ := document["model"].(string)
		requestedModelsLock.Lock()
		requestedModels = append(requestedModels, modelName)
		requestedModelsLock.Unlock()
		if modelName == "vendor/low" {
			http.Error(responseWriter, "provider returned error (502)", http.StatusBadGateway)
			return
		}
		json.NewEncoder(responseWriter).Encode(map[string]any{
			"provider": "example-gateway",
			"model":    modelName,
			"content":  `{"action":"finish"}`,
		})
	}))
	defer server.Close()

	runtimeConfiguration := configuredModelTierRuntime("")
	runtimeConfiguration.Capabilities.Endpoint = server.URL

	providers, errorValue := resolveTaskTierLanguageModelProviders(runtimeConfiguration, slog.New(slog.DiscardHandler))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	response, errorValue := providers.Low.GenerateStructuredResponse(context.Background(), llm.StructuredResponseRequest{
		Messages: []llm.Message{{Role: "user", Content: "test"}},
		StructuredOutputSchema: llm.StructuredOutputSchema{
			Name:     "bluecollar_agent_turn_action",
			Document: `{"type":"object"}`,
		},
	})
	if errorValue != nil {
		t.Fatalf("expected the low tier to escalate to medium and succeed, got error: %v (models requested: %v)", errorValue, requestedModels)
	}
	if !response.UsedFallback {
		t.Fatalf("expected UsedFallback to be marked, got %+v (models requested: %v)", response, requestedModels)
	}
	if response.ModelTier != "medium" {
		t.Fatalf("expected the successful fallback response to report medium tier, got %+v", response)
	}
	requestedModelsLock.Lock()
	defer requestedModelsLock.Unlock()
	if len(requestedModels) < 2 || requestedModels[len(requestedModels)-1] != "vendor/medium" {
		t.Fatalf("expected a final vendor/medium request after vendor/low failures, got %v", requestedModels)
	}
}

func TestCappedHighTierReportsLowModelTier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "/llm/structured") {
			http.Error(responseWriter, "unexpected path "+request.URL.Path, http.StatusNotFound)
			return
		}
		json.NewEncoder(responseWriter).Encode(map[string]any{
			"provider": "example-gateway",
			"model":    "vendor/low",
			"content":  `{"action":"finish"}`,
		})
	}))
	defer server.Close()

	runtimeConfiguration := configuredModelTierRuntime("low")
	runtimeConfiguration.Capabilities.Endpoint = server.URL
	providers, errorValue := resolveTaskTierLanguageModelProviders(runtimeConfiguration, slog.New(slog.DiscardHandler))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	response, errorValue := providers.High.GenerateStructuredResponse(context.Background(), llm.StructuredResponseRequest{
		StructuredOutputSchema: llm.StructuredOutputSchema{Name: "bluecollar_agent_turn_action", Document: `{"type":"object"}`},
	})
	if errorValue != nil {
		t.Fatalf("expected capped high provider to succeed: %v", errorValue)
	}
	if response.ModelTier != "low" {
		t.Fatalf("expected capped high provider to report low tier, got %+v", response)
	}
}
