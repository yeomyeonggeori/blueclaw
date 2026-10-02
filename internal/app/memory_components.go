package app

import (
	"log/slog"
	"time"

	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

const memoryMaintenanceInterval = time.Hour

type memoryComponents struct {
	stores *memory.Stores
}

func newMemoryComponents(runtimeConfiguration config.RuntimeConfiguration, kernel agentKernel, services taskServices, identityService *identity.IdentityService, logger *slog.Logger) memoryComponents {
	logger.Info("application.initializing", "stage", "memory")
	embeddingModelName := firstNonEmptyString(runtimeConfiguration.Memory.EmbeddingModel, llm.DefaultEmbeddingModelName)
	embeddingDimensions := firstPositiveInteger(runtimeConfiguration.Memory.EmbeddingDimensions, llm.DefaultEmbeddingDimensions)
	configuration := bluememo.Configuration{
		Embedder: llm.CapabilityEmbeddingClient{
			CapabilityClient: kernel.capabilityClient,
			ModelName:        embeddingModelName,
			ExecutionMode:    firstNonEmptyString(runtimeConfiguration.Memory.EmbeddingExecutionMode, "auto"),
			OutputDimensions: embeddingDimensions,
		},
		Model:         memory.LanguageModel{Provider: kernel.taskTierLanguageModels.Low},
		RecallSources: true,
		Logger:        logger,
	}
	if kernel.decisionModel != nil {
		configuration.Chooser = memory.Chooser{DecisionModel: kernel.decisionModel}
	}
	workspaceRootPath := firstNonEmptyString(runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace")
	stores := memory.NewStores(workspaceRootPath, configuration, kernel.terminalService.WorkspaceActorFactory())
	if !runtimeConfiguration.Memory.ExtractionDisabled {
		services.taskRunService.RegisterTaskRunTransitionObserver(memory.TaskRunTransitionObserver{
			Stores:   stores,
			TaskRuns: services.taskRunService,
			Steps:    services.taskStepService,
			Access:   identityService,
			Logger:   logger,
		}.Observe)
	}
	logger.Info("application.memory.store_configured",
		"workspaceRoot", workspaceRootPath,
		"embeddingModel", embeddingModelName,
		"embeddingDimensions", embeddingDimensions,
		"extractionDisabled", runtimeConfiguration.Memory.ExtractionDisabled)
	return memoryComponents{stores: stores}
}

func firstPositiveInteger(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
