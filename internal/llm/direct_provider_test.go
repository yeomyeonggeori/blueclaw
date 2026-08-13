package llm

import (
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

func TestDirectProviderRefusesAMissingKeyPathRatherThanReadingTheEnvironment(t *testing.T) {
	runtimeConfiguration := directRuntimeConfiguration(t, "a-key")
	runtimeConfiguration.LanguageModel.Direct.APIKeyPath = ""

	_, errorValue := NewConfiguredLanguageModelProvider(runtimeConfiguration)
	if errorValue == nil {
		t.Fatalf("expected a missing api key path to be refused")
	}
	if !strings.Contains(errorValue.Error(), "api key path") {
		t.Fatalf("expected the refusal to name the key path, got %v", errorValue)
	}
}
