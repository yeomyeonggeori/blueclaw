package app

import (
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type memoryComponents struct {
	memoryService     *memory.MemoryService
	graphReporter     memory.GraphMemoryReporter
	graphMigrator     memory.GraphMemoryMigrator
	pinnedMemoryStore *memory.MarkdownStore
	memoryUpdateQueue *memory.BackgroundMemoryUpdateQueue
}

func newMemoryComponents(runtimeConfiguration config.RuntimeConfiguration, database postgres.Database, compressionLanguageModel model.LanguageModelProvider, logger *slog.Logger) memoryComponents {
	logger.Info("application.initializing", "stage", "memory")
	memoryService := &memory.MemoryService{}
	if strings.TrimSpace(runtimeConfiguration.Memory.GraphitiEndpoint) != "" {
		memoryService.UseGraphStore(memory.NewGraphitiClient(
			runtimeConfiguration.Memory.GraphitiEndpoint,
			time.Duration(runtimeConfiguration.Memory.TimeoutSecond)*time.Second,
		))
	} else {
		logger.Info("application.memory.graph_store_not_configured")
	}
	components := memoryComponents{memoryService: memoryService}
	if database.SQL != nil {
		graphitiMemoryRepository := postgres.NewGraphitiMemoryRepository(database)
		memoryService.UseMirror(graphitiMemoryRepository)
		components.graphReporter = graphitiMemoryRepository
		components.graphMigrator = graphitiMemoryRepository
	}
	components.pinnedMemoryStore = memory.NewMarkdownStore(pinnedMemoryRootPath(runtimeConfiguration), pinnedMemoryHardLimitCharacterCount(runtimeConfiguration))
	components.pinnedMemoryStore.UseCompressor(memory.NewLLMMarkdownMemoryCompressor(compressionLanguageModel), pinnedMemoryCompressionTargetCharacterCount(runtimeConfiguration))
	components.memoryUpdateQueue = memory.NewBackgroundMemoryUpdateQueue(memory.NewMemoryUpdateProcessor(memoryService, components.pinnedMemoryStore), logger)
	return components
}

func pinnedMemoryRootPath(runtimeConfiguration config.RuntimeConfiguration) string {
	if strings.TrimSpace(runtimeConfiguration.Memory.PinnedMemoryRootPath) != "" {
		return strings.TrimSpace(runtimeConfiguration.Memory.PinnedMemoryRootPath)
	}
	return filepath.Join(runtimeConfiguration.Terminal.WorkspaceRootPath, ".blueclaw", "memory")
}

func pinnedMemoryHardLimitCharacterCount(runtimeConfiguration config.RuntimeConfiguration) int {
	if runtimeConfiguration.Memory.PinnedMemoryHardLimitCharacterCount > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryHardLimitCharacterCount
	}
	if runtimeConfiguration.Memory.PinnedMemoryCharacterLimit > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryCharacterLimit
	}
	return memory.DefaultPinnedMemoryHardLimitCharacterCount
}

func pinnedMemoryCompressionTargetCharacterCount(runtimeConfiguration config.RuntimeConfiguration) int {
	if runtimeConfiguration.Memory.PinnedMemoryCompressionTargetCharacterCount > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryCompressionTargetCharacterCount
	}
	return memory.DefaultPinnedMemoryCompressionTargetCharacterCount
}
