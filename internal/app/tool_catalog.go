package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type toolCatalogEndpoint struct {
	resolver     *mcpserver.SessionTokenRequesterResolver
	handler      http.Handler
	approvalGate *approvalgate.Gate
}

func newToolCatalogEndpoint(taskRunService *task.TaskRunService, approvalLanguageModel model.LanguageModelProvider, capabilityClient capability.Client) toolCatalogEndpoint {
	resolver := mcpserver.NewSessionTokenRequesterResolver(newToolCatalogSessionToken)
	handler := mcpserver.NewToolCatalogHandler(resolver, "1")
	approvalGate := approvalgate.New(taskRunService)
	approvalGate.UseLanguageModel(approvalLanguageModel)
	approvalGate.UseApprovalTargetResolver(agentruntime.NewCapabilityApprovalTargetResolver(capabilityClient))
	return toolCatalogEndpoint{resolver: resolver, handler: handler, approvalGate: approvalGate}
}

func newToolCatalogBuilder(runtimeConfiguration config.RuntimeConfiguration, kernel agentKernel, services taskServices, memoryComponents memoryComponents, mcpRegistry *mcp.McpRegistry, logger *slog.Logger) *agentruntime.ToolCatalogBuilder {
	logger.Info("application.initializing", "stage", "tool_catalog")
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
	toolCatalogBuilder.UseMCPQuarantineReporter(func(quarantinedProvider toolcontract.QuarantinedToolProvider) {
		logMCPProviderQuarantine(logger, quarantinedProvider)
	})
	toolCatalogBuilder.UseCapabilityQuarantineReporter(func(quarantinedProvider toolcontract.QuarantinedToolProvider) {
		logCapabilityProviderQuarantine(logger, quarantinedProvider)
	})
	toolCatalogBuilder.UseCapabilityToolDescriptors(kernel.capabilityClient, capabilityToolDescriptors(runtimeConfiguration.Capabilities.ToolDescriptors))
	if runtimeConfiguration.Capabilities.IsConfigured() {
		toolCatalogBuilder.UseRecordCatalog(mcp.NewRecordCatalog(capabilityConfiguration(runtimeConfiguration)))
		toolCatalogBuilder.UseRecordCatalogDivergenceReporter(func(divergence agentruntime.RecordCatalogDivergence) {
			logRecordCatalogDivergence(logger, divergence)
		})
	}
	seedCompanionStatus(kernel.capabilityClient, toolCatalogBuilder)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	toolCatalogBuilder.UseSkillSearch(kernel.skillRetriever, kernel.instructionBundleLoader)
	toolCatalogBuilder.UseTerminalService(kernel.terminalService)
	toolCatalogBuilder.UseTaskRunService(services.taskRunService)
	toolCatalogBuilder.UseTaskArtifactService(services.taskArtifactService)
	toolCatalogBuilder.UseTaskScheduleRepository(services.repositories.taskSchedule)
	toolCatalogBuilder.UseTaskWaitTokenRepository(services.repositories.taskWaitToken)
	toolCatalogBuilder.UseWorkspaceRootPath(runtimeConfiguration.Terminal.WorkspaceRootPath)
	toolCatalogBuilder.UseOptionalFileReadPathSuffixes(runtimeConfiguration.Agent.OptionalFileReadPathSuffixes)
	toolCatalogBuilder.UseSkillChangeHandler(kernel.refreshSkillIndex)
	toolCatalogBuilder.UseMemoryService(memoryComponents.memoryService)
	toolCatalogBuilder.UsePinnedMemoryStore(memoryComponents.pinnedMemoryStore)
	toolCatalogBuilder.UseMemoryUpdateQueue(memoryComponents.memoryUpdateQueue)
	return toolCatalogBuilder
}

func logMCPServerQuarantines(logger *slog.Logger, report mcp.LoadReport) {
	if logger == nil {
		return
	}
	for _, quarantinedServer := range report.Quarantined {
		logger.Warn("mcp.server.quarantined", "serverName", quarantinedServer.Name, "reason", quarantinedServer.Reason)
	}
}

func logMCPProviderQuarantine(logger *slog.Logger, quarantinedProvider toolcontract.QuarantinedToolProvider) {
	if logger == nil {
		return
	}
	logger.Warn("mcp.provider.quarantined", "providerID", quarantinedProvider.ProviderID, "reason", quarantinedProvider.Reason)
}

func logCapabilityProviderQuarantine(logger *slog.Logger, quarantinedProvider toolcontract.QuarantinedToolProvider) {
	if logger == nil {
		return
	}
	logger.Warn("capability.provider.quarantined", "providerID", quarantinedProvider.ProviderID, "reason", quarantinedProvider.Reason)
}

func logRecordCatalogDivergence(logger *slog.Logger, divergence agentruntime.RecordCatalogDivergence) {
	if logger == nil {
		return
	}
	logger.Warn("record.catalog.divergence",
		"requesterEmail", divergence.RequesterEmail,
		"discoveredOnly", divergence.DiscoveredOnly,
		"stampedOnly", divergence.StampedOnly,
		"discoveryFailed", divergence.DiscoveryFailed,
	)
}

func deriveAllowedToolNames(runtimeConfiguration config.RuntimeConfiguration) []string {
	allowedToolNameByName := map[string]bool{}
	for _, toolName := range toolcontract.KernelToolNames() {
		allowedToolNameByName[toolName] = true
	}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		for _, allowedToolName := range agentProfile.AllowedToolNames {
			trimmedToolName := strings.TrimSpace(allowedToolName)
			if toolcontract.IsKernelToolName(trimmedToolName) {
				allowedToolNameByName[trimmedToolName] = true
			}
		}
	}
	allowedToolNames := []string{}
	for allowedToolName := range allowedToolNameByName {
		allowedToolNames = append(allowedToolNames, allowedToolName)
	}
	return allowedToolNames
}

func seedCompanionStatus(capabilityClient capability.Client, toolCatalogBuilder *agentruntime.ToolCatalogBuilder) {
	if strings.TrimSpace(capabilityClient.Endpoint) == "" {
		return
	}
	statusContext, cancelStatus := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStatus()
	var response struct {
		CompanionStatus string `json:"companionStatus"`
	}
	if errorValue := capabilityClient.GetJSON(statusContext, "/v1/capabilities", &response); errorValue != nil {
		return
	}
	toolCatalogBuilder.UseCompanionStatus(response.CompanionStatus)
}

func capabilityToolDescriptors(toolDescriptors []config.CapabilityToolDescriptor) []agentruntime.CapabilityToolDescriptor {
	catalogToolDescriptors := []agentruntime.CapabilityToolDescriptor{}
	for _, toolDescriptor := range toolDescriptors {
		toolDescriptor.Name = strings.TrimSpace(toolDescriptor.Name)
		toolDescriptor.Description = strings.TrimSpace(toolDescriptor.Description)
		if toolDescriptor.Name == "" {
			continue
		}
		catalogToolDescriptors = append(catalogToolDescriptors, toolDescriptor)
	}
	return catalogToolDescriptors
}

func capabilityCompletionEvidence(completionEvidence *config.CapabilityCompletionEvidence) *agentruntime.CapabilityCompletionEvidence {
	if completionEvidence == nil {
		return nil
	}
	return &agentruntime.CapabilityCompletionEvidence{
		Mode:       completionEvidence.Mode,
		Action:     completionEvidence.Action,
		TargetKind: completionEvidence.TargetKind,
	}
}

func deriveAllowedToolNamesByProfile(runtimeConfiguration config.RuntimeConfiguration) map[string][]string {
	allowedToolNamesByProfile := map[string][]string{}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		profileName := strings.TrimSpace(agentProfile.Name)
		if profileName == "" {
			profileName = "default"
		}
		allowedToolNamesByProfile[profileName] = appendDefaultBuiltInToolNames(agentProfile.AllowedToolNames)
	}
	return allowedToolNamesByProfile
}

func appendDefaultBuiltInToolNames(toolNames []string) []string {
	result := toolcontract.KernelToolNames()
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if toolcontract.IsKernelToolName(trimmedToolName) && !containsString(result, trimmedToolName) {
			result = append(result, trimmedToolName)
		}
	}
	return result
}

func containsString(values []string, expectedValue string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expectedValue {
			return true
		}
	}
	return false
}

func newToolCatalogSessionToken() string {
	sessionToken := make([]byte, 32)
	if _, errorValue := rand.Read(sessionToken); errorValue != nil {
		return ""
	}
	return hex.EncodeToString(sessionToken)
}

func toolCatalogURL(runtimeConfiguration config.RuntimeConfiguration) string {
	if configuredURL := strings.TrimSpace(runtimeConfiguration.Agent.Harness.ToolCatalogURL); configuredURL != "" {
		return configuredURL
	}
	return "http://" + deriveListenAddress(runtimeConfiguration.BaseURL) + "/harness/tool-catalog"
}
