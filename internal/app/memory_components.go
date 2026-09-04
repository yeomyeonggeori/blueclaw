package app

import (
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

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
