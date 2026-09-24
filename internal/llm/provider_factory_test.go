package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func endpointConfiguration(url string, modelName string) config.ModelEndpointConfiguration {
	return config.ModelEndpointConfiguration{Endpoint: url, Model: modelName}
}

func configurationWithOneEndpointPerTier(url string, modelName string) config.RuntimeConfiguration {
	tiers := map[string][]config.ModelEndpointConfiguration{}
	for _, modelTier := range ModelTiers {
		tiers[modelTier] = []config.ModelEndpointConfiguration{endpointConfiguration(url, modelName)}
	}
	return config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{Tiers: tiers}}
}

func TestTierProviderFactoryBuildsOneProviderPerConfiguredTier(t *testing.T) {
	tierProviderFactory, errorValue := NewTierProviderFactory(configurationWithOneEndpointPerTier("http://127.0.0.1:9/v1", "example/model"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, modelTier := range ModelTiers {
		tierProvider, tierError := tierProviderFactory(modelTier)
		if tierError != nil {
			t.Fatalf("%s: %v", modelTier, tierError)
		}
		if tierProvider.Provider == nil {
			t.Fatalf("%s reached no provider", modelTier)
		}
		if tierProvider.Reaches != "http://127.0.0.1:9/v1 example/model" {
			t.Fatalf("%s must report the endpoint it reaches, got %q", modelTier, tierProvider.Reaches)
		}
	}
}

func TestTierProviderFactoryRefusesATierItWasGivenNoEndpointFor(t *testing.T) {
	runtimeConfiguration := configurationWithOneEndpointPerTier("http://127.0.0.1:9/v1", "example/model")
	delete(runtimeConfiguration.LanguageModel.Tiers, "high")
	tierProviderFactory, errorValue := NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, tierError := tierProviderFactory("high"); tierError == nil {
		t.Fatal("a tier with no endpoint must be refused rather than silently answered by another tier")
	}
}

func TestATierWithSeveralEndpointsFallsThroughThemInOrder(t *testing.T) {
	runtimeConfiguration := configurationWithOneEndpointPerTier("http://127.0.0.1:9/v1", "example/first")
	runtimeConfiguration.LanguageModel.Tiers["medium"] = []config.ModelEndpointConfiguration{
		endpointConfiguration("http://127.0.0.1:9/v1", "example/first"),
		endpointConfiguration("http://127.0.0.1:9/v1", "example/second"),
	}
	tierProviderFactory, errorValue := NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	tierProvider, errorValue := tierProviderFactory("medium")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	fallbackProvider, isFallbackProvider := tierProvider.Provider.(FallbackLanguageModelProvider)
	if !isFallbackProvider {
		t.Fatalf("a tier given two endpoints must try the second when the first fails, got %T", tierProvider.Provider)
	}
	if _, wouldChainAgain := fallbackProvider.FallbackProvider.(FallbackLanguageModelProvider); wouldChainAgain {
		t.Fatal("two endpoints make one fallback, not two")
	}
	if tierProvider.Reaches != "http://127.0.0.1:9/v1 example/first, http://127.0.0.1:9/v1 example/second" {
		t.Fatalf("the tier must report both endpoints in order, got %q", tierProvider.Reaches)
	}
}

func TestTierProviderFactoryReadsTheKeyOutOfTheFileItWasGiven(t *testing.T) {
	apiKeyPath := filepath.Join(t.TempDir(), "model-key")
	if errorValue := os.WriteFile(apiKeyPath, []byte("  a-key\n"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	runtimeConfiguration := configurationWithOneEndpointPerTier("http://127.0.0.1:9/v1", "example/model")
	runtimeConfiguration.LanguageModel.Tiers["low"] = []config.ModelEndpointConfiguration{{
		Endpoint: "http://127.0.0.1:9/v1", Model: "example/model", APIKeyPath: apiKeyPath,
	}}
	tierProviderFactory, errorValue := NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, tierError := tierProviderFactory("low"); tierError != nil {
		t.Fatal(tierError)
	}
}

func TestTierProviderFactoryRefusesAKeyFileItCannotRead(t *testing.T) {
	runtimeConfiguration := configurationWithOneEndpointPerTier("http://127.0.0.1:9/v1", "example/model")
	runtimeConfiguration.LanguageModel.Tiers["low"] = []config.ModelEndpointConfiguration{{
		Endpoint: "http://127.0.0.1:9/v1", Model: "example/model", APIKeyPath: filepath.Join(t.TempDir(), "absent"),
	}}
	tierProviderFactory, errorValue := NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, tierError := tierProviderFactory("low"); tierError == nil {
		t.Fatal("an endpoint whose key file is missing must be refused rather than asked without a key")
	}
}

func capabilityLadderConfiguration(modelName string) config.RuntimeConfiguration {
	return config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{
		Capability: config.LanguageModelCapabilityConfiguration{
			ExecutionMode: "auto",
			XLowModel:     modelName,
			LowModel:      modelName,
			MediumModel:   modelName,
			HighModel:     modelName,
			XHighModel:    modelName,
			MaxModel:      modelName,
		},
	}}
}

func TestACapabilityLadderReachesEveryTierThroughTheCapabilityRoute(t *testing.T) {
	tierProviderFactory, errorValue := NewTierProviderFactory(capabilityLadderConfiguration("example/model"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, modelTier := range ModelTiers {
		tierProvider, tierError := tierProviderFactory(modelTier)
		if tierError != nil {
			t.Fatalf("%s: %v", modelTier, tierError)
		}
		capabilityClient, isCapabilityClient := tierProvider.Provider.(CapabilityLLMClient)
		if !isCapabilityClient {
			t.Fatalf("%s must reach the capability route, got %T", modelTier, tierProvider.Provider)
		}
		if capabilityClient.ModelName != "example/model" || capabilityClient.ExecutionMode != "auto" {
			t.Fatalf("%s lost the model or the execution mode it was configured with: %+v", modelTier, capabilityClient)
		}
		if capabilityClient.ModelTier != modelTier {
			t.Fatalf("%s must tell capabilityd which tier is asking, got %q", modelTier, capabilityClient.ModelTier)
		}
	}
}

func TestACapabilityLadderMissingATierIsRefused(t *testing.T) {
	runtimeConfiguration := capabilityLadderConfiguration("example/model")
	runtimeConfiguration.LanguageModel.Capability.MaxModel = ""
	tierProviderFactory, errorValue := NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, tierError := tierProviderFactory("max"); tierError == nil {
		t.Fatal("a capability tier with no model must be refused")
	}
}

func TestAConfigurationNamingNeitherShapeIsRefused(t *testing.T) {
	if _, errorValue := NewTierProviderFactory(config.RuntimeConfiguration{}); errorValue == nil {
		t.Fatal("a document naming no endpoints and no capability route must be refused")
	}
}

func TestAConfigurationNamingBothShapesIsRefused(t *testing.T) {
	runtimeConfiguration := configurationWithOneEndpointPerTier("http://127.0.0.1:9/v1", "example/model")
	runtimeConfiguration.LanguageModel.Capability = capabilityLadderConfiguration("example/model").LanguageModel.Capability
	if _, errorValue := NewTierProviderFactory(runtimeConfiguration); errorValue == nil {
		t.Fatal("a document naming both endpoints and the capability route is ambiguous and must be refused")
	}
}

func TestEmbeddingProviderFollowsTheEndpointWhenOneIsNamed(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{
		Embedding: config.ModelEndpointConfiguration{Endpoint: "http://127.0.0.1:9/v1", Model: "example/embedding"},
	}}
	embeddingProvider, errorValue := NewConfiguredEmbeddingProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, isCapabilityClient := embeddingProvider.(CapabilityEmbeddingClient); isCapabilityClient {
		t.Fatal("an embedding endpoint is reached directly, not through the capability route")
	}
}

func TestEmbeddingProviderTakesTheCapabilityRouteWhenOnlyAModelIsNamed(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{
		Embedding:  config.ModelEndpointConfiguration{Model: "example/embedding"},
		Capability: config.LanguageModelCapabilityConfiguration{ExecutionMode: "auto"},
	}}
	embeddingProvider, errorValue := NewConfiguredEmbeddingProvider(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	capabilityClient, isCapabilityClient := embeddingProvider.(CapabilityEmbeddingClient)
	if !isCapabilityClient {
		t.Fatalf("an embedding model with no endpoint is asked for through the capability route, got %T", embeddingProvider)
	}
	if capabilityClient.ModelName != "example/embedding" {
		t.Fatalf("the embedding model must be the one configured, got %q", capabilityClient.ModelName)
	}
}

func TestEmbeddingProviderIsRefusedWhenNoModelIsNamed(t *testing.T) {
	if _, errorValue := NewConfiguredEmbeddingProvider(config.RuntimeConfiguration{}); errorValue == nil {
		t.Fatal("an embedding provider with no model named must be refused")
	}
}

func TestDecisionModelAsksTheEndpointWhenOneIsNamed(t *testing.T) {
	var authorization, modelName string
	decisionServer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		var requestDocument struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(request.Body).Decode(&requestDocument)
		authorization, modelName = request.Header.Get("Authorization"), requestDocument.Model
		_, _ = responseWriter.Write([]byte(`{"answers":{"route":{"choice":"start_task","probabilities":{"start_task":0.9,"answer_question":0.1}}}}`))
	}))
	defer decisionServer.Close()
	apiKeyPath := filepath.Join(t.TempDir(), "decision-key")
	if errorValue := os.WriteFile(apiKeyPath, []byte("decision-key\n"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	runtimeConfiguration := config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{
		Decision: config.ModelEndpointConfiguration{Endpoint: decisionServer.URL + "/v1/systemone", Model: "kev-latest", APIKeyPath: apiKeyPath},
	}}
	decisionModel, errorValue := NewConfiguredDecisionModel(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	response, errorValue := decisionModel.Decide(context.Background(), model.DecisionRequest{
		State:     "a message",
		Questions: map[string]model.DecisionQuestion{"route": model.ChoiceQuestion{Instructions: "What now?", OptionDescriptions: map[string]string{"start_task": "work", "answer_question": "words"}}.Question()},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if authorization != "Bearer decision-key" || modelName != "kev-latest" || response.Answers["route"].Choice != "start_task" {
		t.Fatalf("expected the configured endpoint, key and model to answer; got authorization %q, model %q, answers %+v", authorization, modelName, response.Answers)
	}
}

func TestDecisionModelTakesTheCapabilityRouteWhenNoEndpointIsNamed(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{
		Capability: config.LanguageModelCapabilityConfiguration{DecisionModel: "example/decision", ExecutionMode: "auto"},
	}}
	decisionModel, errorValue := NewConfiguredDecisionModel(runtimeConfiguration)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, isCapabilityClient := decisionModel.(CapabilityDecisionClient); !isCapabilityClient {
		t.Fatalf("a decision model with no endpoint is asked for through the capability route, got %T", decisionModel)
	}
}

func TestDecisionEndpointWithoutAModelIsRefused(t *testing.T) {
	runtimeConfiguration := config.RuntimeConfiguration{LanguageModel: config.LanguageModelConfiguration{
		Decision: config.ModelEndpointConfiguration{Endpoint: "http://127.0.0.1:9/v1/systemone"},
	}}
	if _, errorValue := NewConfiguredDecisionModel(runtimeConfiguration); errorValue == nil {
		t.Fatal("a decision endpoint that names no model must be refused")
	}
}

func TestAnEndpointReadsItsKeyFromTheNamedEnvironmentVariable(t *testing.T) {
	t.Setenv("EXAMPLE_MODEL_KEY", "a-key")
	apiKey, errorValue := endpointAPIKey(config.ModelEndpointConfiguration{Endpoint: "https://example.com/v1", APIKeyEnvironment: "EXAMPLE_MODEL_KEY"})
	if errorValue != nil || apiKey != "a-key" {
		t.Fatalf("expected the key from EXAMPLE_MODEL_KEY, got %q, %v", apiKey, errorValue)
	}
}

func TestAnEndpointRefusesAnUnsetKeyVariableAndASecondKeySource(t *testing.T) {
	t.Setenv("EXAMPLE_MODEL_KEY", "a-key")
	if _, errorValue := endpointAPIKey(config.ModelEndpointConfiguration{Endpoint: "https://example.com/v1", APIKeyEnvironment: "EXAMPLE_UNSET_KEY"}); errorValue == nil || !strings.Contains(errorValue.Error(), "EXAMPLE_UNSET_KEY") {
		t.Fatalf("an unset key variable must be refused by name, got %v", errorValue)
	}
	if _, errorValue := endpointAPIKey(config.ModelEndpointConfiguration{Endpoint: "https://example.com/v1", APIKeyEnvironment: "EXAMPLE_MODEL_KEY", APIKeyPath: "/tmp/key"}); errorValue == nil {
		t.Fatal("an endpoint naming both a key file and a key variable must be refused")
	}
}
