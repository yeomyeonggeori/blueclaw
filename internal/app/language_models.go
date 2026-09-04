package app

import (
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func resolveLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) llm.LanguageModelProvider {
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	if strings.TrimSpace(languageModelConfiguration.LanguageModel.DefaultProvider) == "" {
		return nil
	}

	languageModelProvider, errorValue := llm.NewConfiguredLanguageModelProvider(
		languageModelConfiguration,
	)
	if errorValue != nil {
		return nil
	}

	return languageModelProvider
}

func resolveTaskTierLanguageModelProviders(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) agentcontract.TaskTierLanguageModels {
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	if strings.TrimSpace(languageModelConfiguration.LanguageModel.DefaultProvider) == "" {
		return agentcontract.TaskTierLanguageModels{}
	}
	tierNames := llm.ResolveModelTierNames(languageModelConfiguration)
	maximumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MaximumModelTier)
	minimumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MinimumModelTier)
	if maximumModelTier != "" {
		return resolveCappedTaskTierLanguageModelProviders(languageModelConfiguration, tierNames, minimumModelTier, maximumModelTier, logger)
	}
	if logger != nil {
		logger.Info("resolved task model tiers",
			"max", tierNames.Max,
			"xhigh", tierNames.XHigh,
			"high", tierNames.High,
			"medium", tierNames.Medium,
			"low", tierNames.Low,
			"xlow", tierNames.XLow)
	}
	hasConfigurationError := false
	configuredProvider := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(languageModelConfiguration, modelName)
		if errorValue != nil {
			hasConfigurationError = true
			if logger != nil {
				logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
			}
		}
		return provider
	}
	lowModel := llm.WithModelTier(configuredProvider(tierNames.Low), "low")
	xLowModel := llm.WithModelTier(configuredProvider(tierNames.XLow), "xlow")
	mediumModel := llm.WithModelTier(configuredProvider(tierNames.Medium), "medium")
	highModel := llm.WithModelTier(configuredProvider(tierNames.High), "high")
	xHighModel := llm.WithModelTier(configuredProvider(tierNames.XHigh), "xhigh")
	maxModel := llm.WithModelTier(configuredProvider(tierNames.Max), "max")
	if hasConfigurationError {
		return agentcontract.TaskTierLanguageModels{}
	}

	lowWithFallback := llm.LanguageModelProvider(lowModel)
	if tierNames.Medium != tierNames.Low {
		lowWithFallback = llm.FallbackLanguageModelProvider{
			PrimaryProvider:  lowModel,
			FallbackProvider: mediumModel,
			PrimaryLabel:     "low",
			FallbackLabel:    "medium",
			Logger:           logger,
		}
	}
	xLowWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  xLowModel,
		FallbackProvider: lowWithFallback,
		PrimaryLabel:     "xlow",
		FallbackLabel:    "low",
		Logger:           logger,
	}
	mediumWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  mediumModel,
		FallbackProvider: lowModel,
		PrimaryLabel:     "medium",
		FallbackLabel:    "low",
		Logger:           logger,
	}
	highWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  highModel,
		FallbackProvider: mediumWithFallback,
		PrimaryLabel:     "high",
		FallbackLabel:    "medium",
		Logger:           logger,
	}
	xHighWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  xHighModel,
		FallbackProvider: highWithFallback,
		PrimaryLabel:     "xhigh",
		FallbackLabel:    "high",
		Logger:           logger,
	}
	maxWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  maxModel,
		FallbackProvider: xHighWithFallback,
		PrimaryLabel:     "max",
		FallbackLabel:    "xhigh",
		Logger:           logger,
	}
	return agentcontract.TaskTierLanguageModels{
		Low:    lowWithFallback,
		XLow:   xLowWithFallback,
		Medium: mediumWithFallback,
		High:   highWithFallback,
		XHigh:  xHighWithFallback,
		Max:    maxWithFallback,
	}
}

func resolveIntakeLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) llm.LanguageModelProvider {
	if !runtimeConfiguration.Agent.Intake.Enabled {
		return nil
	}
	executionMode := strings.TrimSpace(runtimeConfiguration.Agent.Intake.ExecutionMode)
	if executionMode == "" {
		executionMode = "auto"
	}
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	languageModelConfiguration.LanguageModel.Capability.ExecutionMode = executionMode
	tierNames := llm.ResolveModelTierNames(languageModelConfiguration)
	maximumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MaximumModelTier)
	hasConfigurationError := false
	configuredProvider := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(languageModelConfiguration, modelName)
		if errorValue == nil {
			return provider
		}
		hasConfigurationError = true
		if logger != nil {
			logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
		}
		return nil
	}
	if maximumModelTier != "" {
		providers := buildCappedModelTierProviders(tierNames, configuredProvider, logger)
		if hasConfigurationError {
			return nil
		}
		return providers.providerForTier(maximumModelTier)
	}
	primaryModelName := firstNonEmptyString(runtimeConfiguration.Agent.Intake.Model, tierNames.Medium)
	primaryProvider := configuredProvider(primaryModelName)
	if strings.TrimSpace(runtimeConfiguration.Agent.Intake.Model) == "" {
		primaryProvider = llm.WithModelTier(primaryProvider, "medium")
	}
	fallbackProvider := llm.WithModelTier(configuredProvider(tierNames.High), "high")
	if hasConfigurationError {
		return nil
	}
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider:  primaryProvider,
		FallbackProvider: fallbackProvider,
		PrimaryLabel:     "intake",
		FallbackLabel:    "high",
		Logger:           logger,
	}
}

type cappedModelTierProviders struct {
	xLow   llm.LanguageModelProvider
	low    llm.LanguageModelProvider
	medium llm.LanguageModelProvider
	high   llm.LanguageModelProvider
	xHigh  llm.LanguageModelProvider
	max    llm.LanguageModelProvider
}

func resolveCappedTaskTierLanguageModelProviders(runtimeConfiguration config.RuntimeConfiguration, tierNames llm.ModelTierNames, minimumModelTier string, maximumModelTier string, logger *slog.Logger) agentcontract.TaskTierLanguageModels {
	hasConfigurationError := false
	providerFactory := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(runtimeConfiguration, modelName)
		if errorValue == nil {
			return provider
		}
		hasConfigurationError = true
		if logger != nil {
			logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
		}
		return nil
	}
	providers := buildCappedModelTierProviders(tierNames, providerFactory, logger)
	if hasConfigurationError {
		return agentcontract.TaskTierLanguageModels{}
	}
	if logger != nil {
		logger.Info("resolved capped task model tiers", "maximumModelTier", maximumModelTier, "xlow", tierNames.XLow, "lowVision", tierNames.Low)
	}
	return agentcontract.TaskTierLanguageModels{
		Low:    providers.providerWithinBounds("low", minimumModelTier, maximumModelTier),
		XLow:   providers.providerWithinBounds("xlow", minimumModelTier, maximumModelTier),
		Medium: providers.providerWithinBounds("medium", minimumModelTier, maximumModelTier),
		High:   providers.providerWithinBounds("high", minimumModelTier, maximumModelTier),
		XHigh:  providers.providerWithinBounds("xhigh", minimumModelTier, maximumModelTier),
		Max:    providers.providerWithinBounds("max", minimumModelTier, maximumModelTier),
	}
}

func buildCappedModelTierProviders(tierNames llm.ModelTierNames, providerFactory func(string) llm.LanguageModelProvider, logger *slog.Logger) cappedModelTierProviders {
	xLowModel := llm.WithModelTier(providerFactory(tierNames.XLow), "xlow")
	lowModel := llm.WithModelTier(providerFactory(tierNames.Low), "low")
	lowProvider := descendingFallbackProvider(lowModel, xLowModel, "low", "xlow", logger)
	xLowProvider := llm.VisionFallbackProvider{TextOnlyModel: xLowModel, VisionModel: lowProvider}
	mediumProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.Medium), "medium"), lowProvider, "medium", "low", logger)
	highProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.High), "high"), mediumProvider, "high", "medium", logger)
	xHighProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.XHigh), "xhigh"), highProvider, "xhigh", "high", logger)
	maxProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.Max), "max"), xHighProvider, "max", "xhigh", logger)
	return cappedModelTierProviders{xLow: xLowProvider, low: lowProvider, medium: mediumProvider, high: highProvider, xHigh: xHighProvider, max: maxProvider}
}

func descendingFallbackProvider(primaryProvider llm.LanguageModelProvider, fallbackProvider llm.LanguageModelProvider, primaryLabel string, fallbackLabel string, logger *slog.Logger) llm.LanguageModelProvider {
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider: primaryProvider, FallbackProvider: fallbackProvider,
		PrimaryLabel: primaryLabel, FallbackLabel: fallbackLabel, Logger: logger,
	}
}

func (providers cappedModelTierProviders) providerWithinBounds(requestedTier string, minimumModelTier string, maximumModelTier string) llm.LanguageModelProvider {
	boundedTier := requestedTier
	if minimumModelTier != "" && modelTierRank(boundedTier) < modelTierRank(minimumModelTier) {
		boundedTier = minimumModelTier
	}
	if modelTierRank(boundedTier) > modelTierRank(maximumModelTier) {
		boundedTier = maximumModelTier
	}
	return providers.providerForTier(boundedTier)
}

func (providers cappedModelTierProviders) providerForTier(modelTier string) llm.LanguageModelProvider {
	switch normalizeMaximumModelTier(modelTier) {
	case "max":
		return providers.max
	case "xhigh":
		return providers.xHigh
	case "high":
		return providers.high
	case "medium":
		return providers.medium
	case "low":
		return providers.low
	default:
		return providers.xLow
	}
}

func normalizeMaximumModelTier(modelTier string) string {
	normalizedModelTier := strings.ToLower(strings.TrimSpace(modelTier))
	for _, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if normalizedModelTier == supportedModelTier {
			return supportedModelTier
		}
	}
	return ""
}

func modelTierRank(modelTier string) int {
	for rank, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if normalizeMaximumModelTier(modelTier) == supportedModelTier {
			return rank
		}
	}
	return 0
}

func deriveLanguageModelRuntimeConfiguration(runtimeConfiguration config.RuntimeConfiguration) config.RuntimeConfiguration {
	if strings.TrimSpace(runtimeConfiguration.LanguageModel.DefaultProvider) == "" {
		runtimeConfiguration.LanguageModel.DefaultProvider = "capabilityLLM"
	}
	runtimeConfiguration.LanguageModel.FallbackProvider = ""
	return runtimeConfiguration
}

func classificationLanguageModelProvider(taskTierLanguageModels agentcontract.TaskTierLanguageModels, intakeLanguageModelProvider model.LanguageModelProvider) model.LanguageModelProvider {
	if taskTierLanguageModels.XLow != nil {
		return taskTierLanguageModels.XLow
	}
	if intakeLanguageModelProvider != nil {
		return intakeLanguageModelProvider
	}
	return taskTierLanguageModels.High
}

func turnRouterLanguageModelProvider(taskTierLanguageModels agentcontract.TaskTierLanguageModels, intakeLanguageModelProvider model.LanguageModelProvider) model.LanguageModelProvider {
	if intakeLanguageModelProvider != nil {
		return intakeLanguageModelProvider
	}
	return taskTierLanguageModels.High
}
