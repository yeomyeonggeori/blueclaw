package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

func TestLegacyModelConfigurationPreservesInitializationFailure(t *testing.T) {
	var configuration config.RuntimeConfiguration
	if errorValue := json.Unmarshal([]byte(`{"languageModel":{"defaultProvider":"capabilityLLM","capability":{"model":"vendor/legacy"}},"agent":{"intake":{"enabled":true}}}`), &configuration); errorValue != nil {
		t.Fatal(errorValue)
	}
	providers, taskError := resolveTaskTierLanguageModelProviders(configuration, nil)
	intakeProvider, intakeError := resolveIntakeLanguageModelProvider(configuration, nil)
	if taskError == nil || intakeError == nil || providers.High != nil || intakeProvider != nil {
		t.Fatalf("invalid model configuration must retain its cause: task=%v intake=%v", taskError, intakeError)
	}
	kernel := agentKernel{taskTierLanguageModels: providers, intakeLanguageModelProvider: intakeProvider, languageModelError: taskError}
	health := languageModelHealth(kernel)
	if health.Configured || !strings.Contains(health.Error, "no tier endpoints and no capability route") {
		t.Fatalf("health lost the provider initialization error: %+v", health)
	}
	handler := newHealthHandler(applicationComponents{kernel: kernel})
	response := httptest.NewRecorder()
	handler.HandleHealth(response, httptest.NewRequest(http.MethodGet, "/admin/api/health", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "language model configuration is not valid") {
		t.Fatalf("invalid model configuration passed health: %d %s", response.Code, response.Body.String())
	}
}

func TestMissingTierFailsEvenWhenOtherTiersAreConfigured(t *testing.T) {
	configuration := configuredModelTierRuntime("")
	configuration.LanguageModel.Capability.MaxModel = ""
	providers, errorValue := resolveTaskTierLanguageModelProviders(configuration, nil)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "max tier no model") || providers.High != nil {
		t.Fatalf("incomplete ladder must fail with its exact cause: %v", errorValue)
	}
}

func TestConfiguredRouterPassesModelReadiness(t *testing.T) {
	configuration := configuredModelTierRuntime("")
	providers, errorValue := resolveTaskTierLanguageModelProviders(configuration, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	health := languageModelHealth(agentKernel{taskTierLanguageModels: providers})
	if !health.Configured || health.Error != "" {
		t.Fatalf("configured router failed readiness: %+v", health)
	}
}
