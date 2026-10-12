package agentruntime

import (
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func newTerminalToolTestCatalogBuilder(workspacePath string) *ToolCatalogBuilder {
	return newTerminalToolTestCatalogBuilderWithNetwork(workspacePath, false)
}

func newTerminalToolTestCatalogBuilderWithNetwork(workspacePath string, allowNetwork bool) *ToolCatalogBuilder {
	terminalService := security.NewShellService(config.TerminalConfiguration{
		WorkspaceRootPath:     workspacePath,
		Mode:                  "native",
		TimeoutSecond:         5,
		OutputMaxBytes:        4096,
		SessionMaxCount:       2,
		AllowInteractiveShell: true,
		AllowNetwork:          allowNetwork,
	})
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseWorkspaceRootPath(workspacePath)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseWorkspaceActorFactory(security.NewDirectWorkspaceActorFactory(terminalService))
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, internalTestToolNames())
	return toolCatalogBuilder
}

func internalTestToolNames() []string {
	return []string{
		"conversation_history",
		"read",
		"document_read",
		"file_deliver",
		"edit",
		"file_preview",
		"file_read",
		"write",
		"image_read",
		"skill_add",
		"skill_remove",
		"skill_search",
		"bash",
	}
}
