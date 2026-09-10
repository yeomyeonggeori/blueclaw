package app

import (
	"log/slog"

	"github.com/yeomyeonggeori/bluememo"
	bluememopostgres "github.com/yeomyeonggeori/bluememo/postgres"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

type memoryComponents struct {
	store     *bluememo.Store
	ingester  *bluememo.Ingester
	jobWorker *bluememo.JobWorker
}

func newMemoryComponents(runtimeConfiguration config.RuntimeConfiguration, database postgres.Database, kernel agentKernel, services taskServices, identityService *identity.IdentityService, logger *slog.Logger) memoryComponents {
	logger.Info("application.initializing", "stage", "memory")
	if database.SQL == nil {
		logger.Info("application.memory.fact_store_not_configured", "reason", "no database")
		return memoryComponents{}
	}
	embeddingModelName := firstNonEmptyString(runtimeConfiguration.Memory.EmbeddingModel, bluememo.DefaultEmbeddingModelName)
	store := &bluememo.Store{
		Facts:    bluememopostgres.NewFactRepository(database.SQL),
		Profiles: bluememopostgres.NewProfileRepository(database.SQL),
		Jobs:     bluememopostgres.NewJobRepository(database.SQL),
		Embedder: llm.CapabilityEmbeddingClient{
			CapabilityClient: kernel.capabilityClient,
			ModelName:        embeddingModelName,
			ExecutionMode:    firstNonEmptyString(runtimeConfiguration.Memory.EmbeddingExecutionMode, "auto"),
			OutputDimensions: bluememo.EmbeddingDimensionCount,
		},
		EmbeddingModel: embeddingModelName,
		Logger:         logger,
	}
	memoryModel := memory.LanguageModel{Provider: kernel.taskTierLanguageModels.Low}
	ingester := &bluememo.Ingester{Store: *store, Model: memoryModel, People: identityService}
	jobWorker := &bluememo.JobWorker{
		Jobs:   store.Jobs,
		Logger: logger,
		Handlers: map[string]bluememo.JobHandler{
			bluememo.JobKindExtract: memory.ExtractJobHandler{Ingester: *ingester, TaskRuns: services.taskRunService, Steps: services.taskStepService, Access: identityService}.Handle,
			bluememo.JobKindProfile: bluememo.ProfileJobHandler{Builder: bluememo.ProfileBuilder{Store: *store, Model: memoryModel}}.Handle,
			bluememo.JobKindReembed: bluememo.ReembedJobHandler{Store: *store}.Handle,
		},
	}
	if !runtimeConfiguration.Memory.ExtractionDisabled {
		services.taskRunService.RegisterTaskRunTransitionObserver(memory.TaskRunTransitionObserver{Store: *store, Logger: logger}.Observe)
	}
	logger.Info("application.memory.fact_store_configured", "embeddingModel", embeddingModelName, "extractionDisabled", runtimeConfiguration.Memory.ExtractionDisabled)
	return memoryComponents{store: store, ingester: ingester, jobWorker: jobWorker}
}
