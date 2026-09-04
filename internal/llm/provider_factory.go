package llm

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

var ModelTiers = []string{"xlow", "low", "medium", "high", "xhigh", "max"}

// TierProvider carries what the tier reaches alongside the provider that
// reaches it, so a caller can see that two tiers land on the same model without
// being told which model that is.
type TierProvider struct {
	Provider LanguageModelProvider
	Reaches  string
}

type TierProviderFactory func(modelTier string) (TierProvider, error)

// NewTierProviderFactory answers with the provider a tier reaches its model
// through. A document that names endpoints is a company plane reaching them
// itself; a document that names the capability route is a device, where
// capabilityd decides between a local model and a remote one.
func NewTierProviderFactory(runtimeConfiguration config.RuntimeConfiguration) (TierProviderFactory, error) {
	languageModelConfiguration := runtimeConfiguration.LanguageModel
	hasEndpoints := len(languageModelConfiguration.Tiers) > 0
	hasCapabilityRoute := capabilityRouteIsNamed(languageModelConfiguration.Capability)
	if hasEndpoints && hasCapabilityRoute {
		return nil, errors.New("the language model configuration names both tier endpoints and the capability route; it must name one")
	}
	if hasEndpoints {
		return endpointTierProviderFactory(languageModelConfiguration.Tiers), nil
	}
	if hasCapabilityRoute {
		return capabilityTierProviderFactory(runtimeConfiguration), nil
	}
	return nil, errors.New("the language model configuration names no tier endpoints and no capability route")
}

func endpointTierProviderFactory(tiers map[string][]config.ModelEndpointConfiguration) TierProviderFactory {
	return func(modelTier string) (TierProvider, error) {
		rungs := tiers[modelTier]
		if len(rungs) == 0 {
			return TierProvider{}, errors.New("the language model configuration gives the " + modelTier + " tier no endpoint")
		}
		var provider LanguageModelProvider
		reached := []string{}
		for rungIndex := len(rungs) - 1; rungIndex >= 0; rungIndex-- {
			rungProvider, errorValue := endpointProvider(rungs[rungIndex])
			if errorValue != nil {
				return TierProvider{}, errorValue
			}
			reached = append([]string{rungs[rungIndex].Endpoint + " " + rungs[rungIndex].Model}, reached...)
			if provider == nil {
				provider = rungProvider
				continue
			}
			provider = FallbackLanguageModelProvider{
				PrimaryProvider:  rungProvider,
				FallbackProvider: provider,
				PrimaryLabel:     modelTier,
				FallbackLabel:    modelTier,
			}
		}
		return TierProvider{Provider: provider, Reaches: strings.Join(reached, ", ")}, nil
	}
}

func endpointProvider(endpointConfiguration config.ModelEndpointConfiguration) (LanguageModelProvider, error) {
	apiKey, errorValue := readAPIKey(endpointConfiguration.APIKeyPath)
	if errorValue != nil {
		return nil, errorValue
	}
	return openaicompatible.Endpoint{
		URL:       endpointConfiguration.Endpoint,
		ModelName: endpointConfiguration.Model,
		APIKey:    apiKey,
	}.Provider()
}

func capabilityTierProviderFactory(runtimeConfiguration config.RuntimeConfiguration) TierProviderFactory {
	return func(modelTier string) (TierProvider, error) {
		modelName := capabilityTierModelName(runtimeConfiguration.LanguageModel.Capability, modelTier)
		if modelName == "" {
			return TierProvider{}, errors.New("the capability language model configuration gives the " + modelTier + " tier no model")
		}
		return TierProvider{
			Provider: NewCapabilityLLMClientForModel(runtimeConfiguration, modelName),
			Reaches:  modelName,
		}, nil
	}
}

func capabilityRouteIsNamed(capabilityConfiguration config.LanguageModelCapabilityConfiguration) bool {
	for _, modelTier := range ModelTiers {
		if capabilityTierModelName(capabilityConfiguration, modelTier) != "" {
			return true
		}
	}
	return false
}

func capabilityTierModelName(capabilityConfiguration config.LanguageModelCapabilityConfiguration, modelTier string) string {
	switch modelTier {
	case "max":
		return strings.TrimSpace(capabilityConfiguration.MaxModel)
	case "xhigh":
		return strings.TrimSpace(capabilityConfiguration.XHighModel)
	case "high":
		return strings.TrimSpace(capabilityConfiguration.HighModel)
	case "medium":
		return strings.TrimSpace(capabilityConfiguration.MediumModel)
	case "low":
		return strings.TrimSpace(capabilityConfiguration.LowModel)
	case "xlow":
		return strings.TrimSpace(capabilityConfiguration.XLowModel)
	default:
		return ""
	}
}

func readAPIKey(apiKeyPath string) (string, error) {
	trimmedPath := strings.TrimSpace(apiKeyPath)
	if trimmedPath == "" {
		return "", nil
	}
	keyDocument, errorValue := os.ReadFile(trimmedPath)
	if errorValue != nil {
		return "", errorValue
	}
	apiKey := strings.TrimSpace(string(keyDocument))
	if apiKey == "" {
		return "", errors.New("the model endpoint's api key file is empty: " + trimmedPath)
	}
	return apiKey, nil
}

func NewConfiguredEmbeddingProvider(runtimeConfiguration config.RuntimeConfiguration) (EmbeddingProvider, error) {
	embeddingConfiguration := runtimeConfiguration.LanguageModel.Embedding
	modelName := strings.TrimSpace(embeddingConfiguration.Model)
	if modelName == "" {
		return nil, errors.New("the language model configuration names no embedding model")
	}
	if strings.TrimSpace(embeddingConfiguration.Endpoint) == "" {
		return CapabilityEmbeddingClient{
			CapabilityClient: newCapabilityClient(runtimeConfiguration),
			ModelName:        modelName,
			ExecutionMode:    runtimeConfiguration.LanguageModel.Capability.ExecutionMode,
		}, nil
	}
	apiKey, errorValue := readAPIKey(embeddingConfiguration.APIKeyPath)
	if errorValue != nil {
		return nil, errorValue
	}
	return openaicompatible.Endpoint{
		URL:       embeddingConfiguration.Endpoint,
		ModelName: modelName,
		APIKey:    apiKey,
	}.EmbeddingProvider()
}

func NewCapabilityLLMClientForModel(runtimeConfiguration config.RuntimeConfiguration, modelName string) CapabilityLLMClient {
	return CapabilityLLMClient{
		CapabilityClient: newCapabilityClient(runtimeConfiguration),
		ModelName:        strings.TrimSpace(modelName),
		ExecutionMode:    runtimeConfiguration.LanguageModel.Capability.ExecutionMode,
	}
}

func newCapabilityClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
		Transport:      runtimeConfiguration.Capabilities.Transport,
		UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
		VSockCID:       runtimeConfiguration.Capabilities.VSockCID,
		VSockPort:      runtimeConfiguration.Capabilities.VSockPort,
		Timeout:        time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
	})
}
