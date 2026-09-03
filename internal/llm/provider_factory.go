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

func NewConfiguredLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) (LanguageModelProvider, error) {
	return NewConfiguredLanguageModelProviderForModel(runtimeConfiguration, capabilityModelName(runtimeConfiguration))
}

func NewConfiguredLanguageModelProviderForModel(runtimeConfiguration config.RuntimeConfiguration, modelName string) (LanguageModelProvider, error) {
	defaultProvider, errorValue := providerByName(runtimeConfiguration.LanguageModel.DefaultProvider, runtimeConfiguration, modelName)
	if errorValue != nil {
		return nil, errorValue
	}

	if strings.TrimSpace(runtimeConfiguration.LanguageModel.FallbackProvider) == "" {
		return defaultProvider, nil
	}

	return nil, errors.New("language model fallback is owned by the capability runtime, not by blueclaw configuration")
}

func providerByName(providerName string, runtimeConfiguration config.RuntimeConfiguration, modelName string) (LanguageModelProvider, error) {
	switch strings.TrimSpace(providerName) {
	case "capabilityLLM", "capability", "":
		return NewCapabilityLLMClientForModel(runtimeConfiguration, modelName), nil
	case "direct":
		return newDirectProvider(runtimeConfiguration, modelName)
	default:
		return nil, errors.New("language model provider is not supported")
	}
}

func newDirectProvider(runtimeConfiguration config.RuntimeConfiguration, modelName string) (LanguageModelProvider, error) {
	directConfiguration := runtimeConfiguration.LanguageModel.Direct
	endpoint := strings.TrimSpace(directConfiguration.Endpoint)
	if endpoint == "" {
		return nil, errors.New("the direct language model provider has no endpoint")
	}
	apiKey, errorValue := readAPIKey(directConfiguration.APIKeyPath)
	if errorValue != nil {
		return nil, errorValue
	}
	chosenModel := firstNonEmptyModelName(modelName, ResolveModelTierNames(runtimeConfiguration).Medium)
	return openaicompatible.NewProvider(endpoint, apiKey, chosenModel), nil
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
		return "", errors.New("the direct language model provider's api key file is empty")
	}
	return apiKey, nil
}

func NewCapabilityLLMClientForModel(runtimeConfiguration config.RuntimeConfiguration, modelName string) CapabilityLLMClient {
	return CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
			Transport:      runtimeConfiguration.Capabilities.Transport,
			UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
			VSockCID:       runtimeConfiguration.Capabilities.VSockCID,
			VSockPort:      runtimeConfiguration.Capabilities.VSockPort,
			Timeout:        time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
		}),
		ModelName:     strings.TrimSpace(modelName),
		ExecutionMode: runtimeConfiguration.LanguageModel.Capability.ExecutionMode,
	}
}

func capabilityModelName(runtimeConfiguration config.RuntimeConfiguration) string {
	return strings.TrimSpace(runtimeConfiguration.LanguageModel.Capability.Model)
}

// One model answers every tier. The tiers still mean what they meant — how much
// budget and how many steps a turn is given — and a company that wants a
// different model at a tier still says so in its runtime configuration.
const defaultModelName = "z-ai/glm-5.3-flash"

const (
	defaultMaxModelName    = defaultModelName
	defaultXHighModelName  = defaultModelName
	defaultHighModelName   = defaultModelName
	defaultMediumModelName = defaultModelName
	defaultLowModelName    = defaultModelName
	defaultXLowModelName   = defaultModelName
)

type ModelTierNames struct {
	Max    string
	XHigh  string
	High   string
	Medium string
	Low    string
	XLow   string
}

func ResolveModelTierNames(runtimeConfiguration config.RuntimeConfiguration) ModelTierNames {
	capabilityConfiguration := runtimeConfiguration.LanguageModel.Capability
	directModelName := runtimeConfiguration.LanguageModel.Direct.Model
	return ModelTierNames{
		Max:    firstNonEmptyModelName(capabilityConfiguration.MaxModel, directModelName, defaultMaxModelName),
		XHigh:  firstNonEmptyModelName(capabilityConfiguration.XHighModel, directModelName, defaultXHighModelName),
		High:   firstNonEmptyModelName(capabilityConfiguration.HighModel, directModelName, defaultHighModelName),
		Medium: firstNonEmptyModelName(capabilityConfiguration.MediumModel, directModelName, defaultMediumModelName),
		Low:    firstNonEmptyModelName(capabilityConfiguration.LowModel, directModelName, defaultLowModelName),
		XLow:   firstNonEmptyModelName(capabilityConfiguration.XLowModel, directModelName, defaultXLowModelName),
	}
}

func firstNonEmptyModelName(candidates ...string) string {
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// DefaultModelTierNames answers the tier names a deployment gets before it
// configures any, so a caller that has no runtime configuration - a test, or a
// standalone run - does not have to build an empty one to ask.
func DefaultModelTierNames() ModelTierNames {
	return ResolveModelTierNames(config.RuntimeConfiguration{})
}
