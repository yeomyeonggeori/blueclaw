package llm

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
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
	case "llmd":
		return newLLMDClient(runtimeConfiguration, modelName)
	default:
		return nil, errors.New("language model provider is not supported")
	}
}

func newLLMDClient(runtimeConfiguration config.RuntimeConfiguration, modelName string) (LLMDClient, error) {
	llmdConfiguration := runtimeConfiguration.LanguageModel.LLMD
	authKey := ""
	authKeyPath := strings.TrimSpace(llmdConfiguration.AuthKeyPath)
	if authKeyPath == "" && !isLLMDBridgeConfiguration(llmdConfiguration) {
		return LLMDClient{}, errors.New("llmd auth key path is not configured")
	}
	if authKeyPath != "" {
		authKeyDocument, errorValue := os.ReadFile(authKeyPath)
		if errorValue != nil {
			return LLMDClient{}, errorValue
		}
		authKey = strings.TrimSpace(string(authKeyDocument))
		if authKey == "" {
			return LLMDClient{}, errors.New("llmd auth key is empty")
		}
	}
	clientConfiguration := LLMDClientConfiguration{
		Endpoint:                        llmdConfiguration.Endpoint,
		UnixSocketPath:                  llmdConfiguration.UnixSocketPath,
		AuthKey:                         authKey,
		ModelName:                       modelName,
		ExecutionMode:                   firstNonEmptyModelName(llmdConfiguration.ExecutionMode, runtimeConfiguration.LanguageModel.Capability.ExecutionMode),
		LocalOnly:                       llmdConfiguration.LocalOnly,
		StructuredSchemaNames:           configuredLLMDSchemaNames(runtimeConfiguration),
		IsStructuredOutputAuthoritative: strings.TrimSpace(runtimeConfiguration.LanguageModel.DefaultProvider) == "llmd",
	}
	if HasCapabilityEndpoint(runtimeConfiguration) {
		capabilityProvider := NewCapabilityLLMClientForModel(runtimeConfiguration, modelName)
		clientConfiguration.TextProvider = capabilityProvider
		clientConfiguration.StructuredFallbackProvider = capabilityProvider
	}
	return NewLLMDClient(clientConfiguration), nil
}

func HasCapabilityEndpoint(runtimeConfiguration config.RuntimeConfiguration) bool {
	return runtimeConfiguration.Capabilities.IsConfigured()
}

func isLLMDBridgeConfiguration(configuration config.LanguageModelLLMDConfiguration) bool {
	normalizedEndpoint := strings.TrimRight(strings.TrimSpace(configuration.Endpoint), "/")
	if normalizedEndpoint == llmdLoopbackBridgeEndpoint {
		return true
	}
	if strings.TrimSpace(configuration.UnixSocketPath) == "" {
		return false
	}
	parsedEndpoint, errorValue := url.Parse(normalizedEndpoint)
	if errorValue != nil {
		return false
	}
	return parsedEndpoint.Scheme == "http" &&
		parsedEndpoint.Host != "" &&
		parsedEndpoint.Path == "/_internkim/llmd" &&
		parsedEndpoint.RawQuery == "" &&
		parsedEndpoint.Fragment == ""
}

func configuredLLMDSchemaNames(runtimeConfiguration config.RuntimeConfiguration) []string {
	configuredSchemaNames := runtimeConfiguration.LanguageModel.LLMD.StructuredSchemaNames
	if len(configuredSchemaNames) == 0 {
		return DefaultLLMDStructuredSchemaNames()
	}
	return append([]string{}, configuredSchemaNames...)
}

func DefaultLLMDStructuredSchemaNames() []string {
	return []string{
		"bluecollar_agent_turn_action",
		"bluecollar_agent_turn_finalizer",
		"bluecollar_turn_router",
		"bluecollar_recovery_decision",
		"bluecollar_contract_skill_arbitration",
		"bluecollar_completion_judge",
	}
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

const (
	defaultMaxModelName    = "google/gemini-3.6-flash"
	defaultXHighModelName  = "google/gemini-3.5-flash-lite"
	defaultHighModelName   = "google/gemini-3.5-flash-lite"
	defaultMediumModelName = "google/gemini-3.1-flash-lite"
	defaultLowModelName    = "openai/gpt-5.6-luna"
	defaultXLowModelName   = "openai/gpt-5.6-luna"
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
	return ModelTierNames{
		Max:    firstNonEmptyModelName(capabilityConfiguration.MaxModel, defaultMaxModelName),
		XHigh:  firstNonEmptyModelName(capabilityConfiguration.XHighModel, defaultXHighModelName),
		High:   firstNonEmptyModelName(capabilityConfiguration.HighModel, defaultHighModelName),
		Medium: firstNonEmptyModelName(capabilityConfiguration.MediumModel, defaultMediumModelName),
		Low:    firstNonEmptyModelName(capabilityConfiguration.LowModel, defaultLowModelName),
		XLow:   firstNonEmptyModelName(capabilityConfiguration.XLowModel, defaultXLowModelName),
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
