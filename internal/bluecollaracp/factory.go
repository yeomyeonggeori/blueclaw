package bluecollaracp

import (
	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/bluecollarharness"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/acpagent"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

var ledgerEventNamesBlueclawWritesItself = []string{
	agentcontract.TaskEventTaskSteerRequested,
}

func NewFactory(toolCatalogPublisher acpharness.ToolCatalogPublisher) harnessdriver.Factory {
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		skillRetriever := bluecollarharness.NewSkillRetriever(dependencies)
		harness := acpharness.New(agentProcess{dependencies: dependencies, skillRetriever: skillRetriever}, toolCatalogPublisher, dependencies.TaskRunStore)
		harness.UseToolAudience(mcpserver.ToolAudienceBare)
		harness.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
		harness.UseHostInstruction()
		harness.UsePromptMeta(promptMeta)
		harness.UseCheckpointMarker(acpagent.CheckpointMetaKey)
		harness.UseTurnResultMeta(acpagent.TurnResultMetaKey)
		harness.UseLedgerExchange(skippedLedgerEventNames(dependencies))
		return harness, skillRetriever
	}
}

func promptMeta(request agentcontract.AgentTurnRequest) map[string]any {
	promptMeta := map[string]any{acpagent.TurnRequestMetaKey: handedOverRequest(request)}
	if request.ExistingTaskRunID != "" {
		promptMeta[acpagent.TaskRunMetaKey] = request.ExistingTaskRunID
	}
	return promptMeta
}

func handedOverRequest(request agentcontract.AgentTurnRequest) agentcontract.AgentTurnRequest {
	request.ToolSet = nil
	request.AvailableSkills = nil
	request.PinnedToolNames = nil
	request.PinnedSkillNames = nil
	request.HostInstruction = ""
	request.InstructionPrompt = ""
	request.InstructionSources = nil
	request.MemoryFacts = nil
	request.CarriedOutCalls = nil
	request.InputParts = partsWithoutImages(request.InputParts)
	return request
}

func partsWithoutImages(parts []agentcontract.AgentPart) []agentcontract.AgentPart {
	kept := []agentcontract.AgentPart{}
	for _, part := range parts {
		if part.Type != agentcontract.AgentPartTypeImage {
			kept = append(kept, part)
		}
	}
	return kept
}

func skippedLedgerEventNames(dependencies harnessdriver.Dependencies) []string {
	skippedNames := append([]string{}, ledgerEventNamesBlueclawWritesItself...)
	if dependencies.LLMCallRepository != nil {
		skippedNames = append(skippedNames, agentcontract.TaskEventLLMCall)
	}
	return skippedNames
}
