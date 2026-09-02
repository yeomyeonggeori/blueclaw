package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

func directRuntimeConfiguration(t *testing.T, apiKey string) config.RuntimeConfiguration {
	t.Helper()
	apiKeyPath := filepath.Join(t.TempDir(), "api-key")
	if errorValue := os.WriteFile(apiKeyPath, []byte(apiKey), 0o600); errorValue != nil {
		t.Fatalf("expected the api key to be written: %v", errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.DefaultProvider = "direct"
	runtimeConfiguration.LanguageModel.Direct.Endpoint = "https://openrouter.ai/api/v1"
	runtimeConfiguration.LanguageModel.Direct.APIKeyPath = apiKeyPath
	runtimeConfiguration.LanguageModel.Direct.Model = "moonshotai/kimi-k2-thinking"
	return runtimeConfiguration
}

func TestDirectProviderReachesTheModelWithoutASidecar(t *testing.T) {
	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(directRuntimeConfiguration(t, "a-key\n"))
	if errorValue != nil {
		t.Fatalf("expected provider to be created: %v", errorValue)
	}
	if _, isDirectProvider := languageModelProvider.(*openaicompatible.Provider); !isDirectProvider {
		t.Fatalf("expected bluecollar's openai-compatible provider, got %T", languageModelProvider)
	}
}

func TestDirectProviderTakesTheModelTheCallerAskedFor(t *testing.T) {
	runtimeConfiguration := directRuntimeConfiguration(t, "a-key")

	languageModelProvider, errorValue := NewConfiguredLanguageModelProviderForModel(runtimeConfiguration, "anthropic/claude-opus-5")
	if errorValue != nil {
		t.Fatalf("expected provider to be created: %v", errorValue)
	}
	if _, isDirectProvider := languageModelProvider.(*openaicompatible.Provider); !isDirectProvider {
		t.Fatalf("expected bluecollar's openai-compatible provider, got %T", languageModelProvider)
	}
}

func TestDirectProviderRefusesAnEmptyKeyRatherThanCallingUnauthenticated(t *testing.T) {
	_, errorValue := NewConfiguredLanguageModelProvider(directRuntimeConfiguration(t, "   \n"))
	if errorValue == nil {
		t.Fatalf("expected an empty api key file to be refused")
	}
	if !strings.Contains(errorValue.Error(), "api key file is empty") {
		t.Fatalf("expected the refusal to name the empty key, got %v", errorValue)
	}
}

func TestDirectProviderFallsBackToATierModelRatherThanNoProvider(t *testing.T) {
	runtimeConfiguration := directRuntimeConfiguration(t, "a-key")
	runtimeConfiguration.LanguageModel.Direct.Model = ""

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected a configured model to be found: %v", errorValue)
	}
	if _, isDirectProvider := languageModelProvider.(*openaicompatible.Provider); !isDirectProvider {
		t.Fatalf("expected bluecollar's openai-compatible provider, got %T", languageModelProvider)
	}
}

func TestDirectProviderRefusesAMissingEndpoint(t *testing.T) {
	runtimeConfiguration := directRuntimeConfiguration(t, "a-key")
	runtimeConfiguration.LanguageModel.Direct.Endpoint = "  "

	_, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue == nil {
		t.Fatalf("expected a missing endpoint to be refused")
	}
	if !strings.Contains(errorValue.Error(), "no endpoint") {
		t.Fatalf("expected the refusal to name the endpoint, got %v", errorValue)
	}
}

func TestDirectProviderCallsUnauthenticatedRatherThanReadingTheEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "an-environment-key")
	receivedAuthorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		receivedAuthorization <- request.Header.Get("Authorization")
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"answer","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()

	runtimeConfiguration := directRuntimeConfiguration(t, "a-key")
	runtimeConfiguration.LanguageModel.Direct.Endpoint = server.URL
	runtimeConfiguration.LanguageModel.Direct.APIKeyPath = ""

	languageModelProvider, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatalf("expected a local server that authenticates nobody to be reachable: %v", errorValue)
	}
	request := StructuredResponseRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: StructuredOutputSchema{
			Name:     "answer",
			Document: `{"type":"object","properties":{},"additionalProperties":false}`,
		},
	}
	if _, errorValue := languageModelProvider.GenerateStructuredResponse(context.Background(), request); errorValue != nil {
		t.Fatalf("expected the call to reach the endpoint: %v", errorValue)
	}
	if authorization := <-receivedAuthorization; authorization != "" {
		t.Fatalf("expected no authorization header when no key path is configured, got %q", authorization)
	}
}
