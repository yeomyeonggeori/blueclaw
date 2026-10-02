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

// defaultMemoryEmbedding names a model and the width it answers in together,
// because a store embedded at one width cannot be searched at another and the
// two are set in different places otherwise.
var defaultMemoryEmbedding = struct {
	ModelName  string
	Dimensions int
}{ModelName: "baai/bge-m3", Dimensions: 1024}

type memoryComponents struct {
	stores *memory.Stores
}

func newMemoryComponents(runtimeConfiguration config.RuntimeConfiguration, kernel agentKernel, services taskServices, identityService *identity.IdentityService, logger *slog.Logger) memoryComponents {
	logger.Info("application.initializing", "stage", "memory")
	embeddingModelName := firstNonEmptyString(runtimeConfiguration.Memory.EmbeddingModel, defaultMemoryEmbedding.ModelName)
	configuration := bluememo.Configuration{
		Embedder: llm.CapabilityEmbeddingClient{
			CapabilityClient: kernel.capabilityClient,
			ModelName:        embeddingModelName,
			ExecutionMode:    firstNonEmptyString(runtimeConfiguration.Memory.EmbeddingExecutionMode, "auto"),
			OutputDimensions: firstPositiveInteger(runtimeConfiguration.Memory.EmbeddingDimensions, defaultMemoryEmbedding.Dimensions),
		},
		EmbeddingModel: embeddingModelName,
		Model:          memory.LanguageModel{Provider: kernel.taskTierLanguageModels.Low},
		RecallSources:  true,
		Logger:         logger,
	}
	if kernel.decisionModel != nil {
		configuration.Judge = bluememo.DistributionJudge{Chooser: memory.Chooser{DecisionModel: kernel.decisionModel}}
	}
	workspaceRootPath := firstNonEmptyString(runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace")
	stores := memory.NewStores(workspaceRootPath, configuration)
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
		"embeddingDimensions", firstPositiveInteger(runtimeConfiguration.Memory.EmbeddingDimensions, defaultMemoryEmbedding.Dimensions),
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
