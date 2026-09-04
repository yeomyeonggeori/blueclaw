package app

import (
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func resolveTaskTierLanguageModelProviders(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) agentcontract.TaskTierLanguageModels {
	providers, errorValue := resolveModelTierProviders(runtimeConfiguration, logger)
	if errorValue != nil {
		return agentcontract.TaskTierLanguageModels{}
	}
	minimumModelTier := normalizeModelTier(runtimeConfiguration.LanguageModel.MinimumModelTier)
	maximumModelTier := normalizeModelTier(runtimeConfiguration.LanguageModel.MaximumModelTier)
	if maximumModelTier == "" {
		return agentcontract.TaskTierLanguageModels{
			XLow:   providers.xLow,
			Low:    providers.low,
			Medium: providers.medium,
			High:   providers.high,
			XHigh:  providers.xHigh,
			Max:    providers.max,
		}
	}
	return agentcontract.TaskTierLanguageModels{
		XLow:   providers.providerWithinBounds("xlow", minimumModelTier, maximumModelTier),
		Low:    providers.providerWithinBounds("low", minimumModelTier, maximumModelTier),
		Medium: providers.providerWithinBounds("medium", minimumModelTier, maximumModelTier),
		High:   providers.providerWithinBounds("high", minimumModelTier, maximumModelTier),
		XHigh:  providers.providerWithinBounds("xhigh", minimumModelTier, maximumModelTier),
		Max:    providers.providerWithinBounds("max", minimumModelTier, maximumModelTier),
	}
}

func resolveIntakeLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) llm.LanguageModelProvider {
	if !runtimeConfiguration.Agent.Intake.Enabled {
		return nil
	}
	intakeConfiguration := runtimeConfiguration
	intakeConfiguration.LanguageModel.Capability.ExecutionMode = firstNonEmptyString(
		runtimeConfiguration.Agent.Intake.ExecutionMode,
		runtimeConfiguration.LanguageModel.Capability.ExecutionMode,
	)
	providers, errorValue := resolveModelTierProviders(intakeConfiguration, logger)
	if errorValue != nil {
		return nil
	}
	if maximumModelTier := normalizeModelTier(runtimeConfiguration.LanguageModel.MaximumModelTier); maximumModelTier != "" {
		return providers.providerForTier(maximumModelTier)
	}
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider:  providers.mediumTierOnly,
		FallbackProvider: providers.highTierOnly,
		PrimaryLabel:     "intake",
		FallbackLabel:    "high",
		Logger:           logger,
	}
}

type modelTierProviders struct {
	xLow           llm.LanguageModelProvider
	low            llm.LanguageModelProvider
	medium         llm.LanguageModelProvider
	high           llm.LanguageModelProvider
	xHigh          llm.LanguageModelProvider
	max            llm.LanguageModelProvider
	mediumTierOnly llm.LanguageModelProvider
	highTierOnly   llm.LanguageModelProvider
}

func resolveModelTierProviders(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) (modelTierProviders, error) {
	tierProviderFactory, errorValue := llm.NewTierProviderFactory(runtimeConfiguration)
	if errorValue != nil {
		logModelTierFailure(logger, "", errorValue)
		return modelTierProviders{}, errorValue
	}
	tiers := map[string]llm.TierProvider{}
	for _, modelTier := range llm.ModelTiers {
		tierProvider, tierError := tierProviderFactory(modelTier)
		if tierError != nil {
			logModelTierFailure(logger, modelTier, tierError)
			return modelTierProviders{}, tierError
		}
		tiers[modelTier] = tierProvider
	}
	logResolvedModelTiers(logger, tiers)
	if normalizeModelTier(runtimeConfiguration.LanguageModel.MaximumModelTier) != "" {
		return cappedModelTierProviders(tiers, logger), nil
	}
	return uncappedModelTierProviders(tiers, logger), nil
}

// A capped ladder is a machine that cannot run its upper tiers, so the lowest
// tier climbs for an image instead of answering blind, and every tier above it
// degrades downward.
func cappedModelTierProviders(tiers map[string]llm.TierProvider, logger *slog.Logger) modelTierProviders {
	xLowModel := taggedTierProvider(tiers, "xlow")
	lowProvider := descendingFallbackProvider(taggedTierProvider(tiers, "low"), xLowModel, "low", "xlow", logger)
	mediumTierOnly := taggedTierProvider(tiers, "medium")
	highTierOnly := taggedTierProvider(tiers, "high")
	mediumProvider := descendingFallbackProvider(mediumTierOnly, lowProvider, "medium", "low", logger)
	highProvider := descendingFallbackProvider(highTierOnly, mediumProvider, "high", "medium", logger)
	xHighProvider := descendingFallbackProvider(taggedTierProvider(tiers, "xhigh"), highProvider, "xhigh", "high", logger)
	return modelTierProviders{
		xLow:           llm.VisionFallbackProvider{TextOnlyModel: xLowModel, VisionModel: lowProvider},
		low:            lowProvider,
		medium:         mediumProvider,
		high:           highProvider,
		xHigh:          xHighProvider,
		max:            descendingFallbackProvider(taggedTierProvider(tiers, "max"), xHighProvider, "max", "xhigh", logger),
		mediumTierOnly: mediumTierOnly,
		highTierOnly:   highTierOnly,
	}
}

func uncappedModelTierProviders(tiers map[string]llm.TierProvider, logger *slog.Logger) modelTierProviders {
	lowTierOnly := taggedTierProvider(tiers, "low")
	mediumTierOnly := taggedTierProvider(tiers, "medium")
	highTierOnly := taggedTierProvider(tiers, "high")
	lowProvider := lowTierOnly
	if tiers["medium"].Reaches != tiers["low"].Reaches {
		lowProvider = descendingFallbackProvider(lowTierOnly, mediumTierOnly, "low", "medium", logger)
	}
	mediumProvider := descendingFallbackProvider(mediumTierOnly, lowTierOnly, "medium", "low", logger)
	highProvider := descendingFallbackProvider(highTierOnly, mediumProvider, "high", "medium", logger)
	xHighProvider := descendingFallbackProvider(taggedTierProvider(tiers, "xhigh"), highProvider, "xhigh", "high", logger)
	return modelTierProviders{
		xLow:           descendingFallbackProvider(taggedTierProvider(tiers, "xlow"), lowProvider, "xlow", "low", logger),
		low:            lowProvider,
		medium:         mediumProvider,
		high:           highProvider,
		xHigh:          xHighProvider,
		max:            descendingFallbackProvider(taggedTierProvider(tiers, "max"), xHighProvider, "max", "xhigh", logger),
		mediumTierOnly: mediumTierOnly,
		highTierOnly:   highTierOnly,
	}
}

func taggedTierProvider(tiers map[string]llm.TierProvider, modelTier string) llm.LanguageModelProvider {
	return llm.WithModelTier(tiers[modelTier].Provider, modelTier)
}

func descendingFallbackProvider(primaryProvider llm.LanguageModelProvider, fallbackProvider llm.LanguageModelProvider, primaryLabel string, fallbackLabel string, logger *slog.Logger) llm.LanguageModelProvider {
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider: primaryProvider, FallbackProvider: fallbackProvider,
		PrimaryLabel: primaryLabel, FallbackLabel: fallbackLabel, Logger: logger,
	}
}

func (providers modelTierProviders) providerWithinBounds(requestedTier string, minimumModelTier string, maximumModelTier string) llm.LanguageModelProvider {
	boundedTier := requestedTier
	if minimumModelTier != "" && modelTierRank(boundedTier) < modelTierRank(minimumModelTier) {
		boundedTier = minimumModelTier
	}
	if modelTierRank(boundedTier) > modelTierRank(maximumModelTier) {
		boundedTier = maximumModelTier
	}
	return providers.providerForTier(boundedTier)
}

func (providers modelTierProviders) providerForTier(modelTier string) llm.LanguageModelProvider {
	switch normalizeModelTier(modelTier) {
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

func normalizeModelTier(modelTier string) string {
	normalizedModelTier := strings.ToLower(strings.TrimSpace(modelTier))
	for _, supportedModelTier := range llm.ModelTiers {
		if normalizedModelTier == supportedModelTier {
			return supportedModelTier
		}
	}
	return ""
}

func modelTierRank(modelTier string) int {
	for rank, supportedModelTier := range llm.ModelTiers {
		if normalizeModelTier(modelTier) == supportedModelTier {
			return rank
		}
	}
	return 0
}

func logResolvedModelTiers(logger *slog.Logger, tiers map[string]llm.TierProvider) {
	if logger == nil {
		return
	}
	attributes := []any{}
	for _, modelTier := range llm.ModelTiers {
		attributes = append(attributes, modelTier, tiers[modelTier].Reaches)
	}
	logger.Info("resolved task model tiers", attributes...)
}

func logModelTierFailure(logger *slog.Logger, modelTier string, errorValue error) {
	if logger == nil {
		return
	}
	logger.Error("language model provider configuration failed", "tier", modelTier, "error", errorValue.Error())
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
