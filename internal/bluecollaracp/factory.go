package bluecollaracp

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/acpagent"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type ProcessFor func(dependencies harnessdriver.Dependencies, skillRetriever agentcontract.SkillRetriever) acpharness.AgentProcess

func NewFactory(toolCatalogPublisher acpharness.ToolCatalogPublisher) harnessdriver.Factory {
	return NewFactoryOverProcess(inProcessAgent)(toolCatalogPublisher)
}

func NewFactoryOverProcess(processFor ProcessFor) harnessdriver.ACPFactory {
	return func(toolCatalogPublisher acpharness.ToolCatalogPublisher) harnessdriver.Factory {
		return factoryOver(processFor, toolCatalogPublisher)
	}
}

func inProcessAgent(dependencies harnessdriver.Dependencies, skillRetriever agentcontract.SkillRetriever) acpharness.AgentProcess {
	return agentProcess{dependencies: dependencies, skillRetriever: skillRetriever}
}

func factoryOver(processFor ProcessFor, toolCatalogPublisher acpharness.ToolCatalogPublisher) harnessdriver.Factory {
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		skillRetriever := newSkillRetriever(dependencies)
		harness := acpharness.New(processFor(dependencies, skillRetriever), toolCatalogPublisher, dependencies.TaskRunStore)
		harness.UseToolAudience(mcpserver.ToolAudienceBare)
		harness.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
		harness.UseHostInstruction()
		harness.UsePromptMeta(promptMetaFor(dependencies))
		harness.UseCheckpointMarker(acpagent.CheckpointMetaKey)
		harness.UseTurnResultMeta(acpagent.TurnResultMetaKey)
		harness.UseLedgerExchange(skippedLedgerEventNames(dependencies))
		harness.UseTurnContextOnToolCalls()
		harness.UseSteerExtension(acpagent.SteerMethod, steerNotification)
		return harness, skillRetriever
	}
}

func steerNotification(sessionID acp.SessionId, steer acpharness.SteerRequest) any {
	return acpagent.SteerNotification{SessionID: sessionID, Instruction: steer.Instruction, Reason: steer.Reason, MessageID: steer.MessageID}
}

func promptMetaFor(dependencies harnessdriver.Dependencies) func(agentcontract.AgentTurnRequest) map[string]any {
	return func(request agentcontract.AgentTurnRequest) map[string]any {
		promptMeta := map[string]any{acpagent.TurnRequestMetaKey: handedOverRequest(request)}
		if request.ExistingTaskRunID != "" {
			promptMeta[acpagent.TaskRunMetaKey] = request.ExistingTaskRunID
		}
		if dependencies.InstructionBundleLoader != nil {
			promptMeta[acpagent.InstructionBundleMetaKey] = dependencies.InstructionBundleLoader()
		}
		return promptMeta
	}
}

func handedOverRequest(request agentcontract.AgentTurnRequest) agentcontract.AgentTurnRequest {
	request.ToolSet = nil
	request.AvailableSkills = nil
	request.PinnedToolNames = nil
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
	skippedNames := []string{}
	if dependencies.LLMCallRepository != nil {
		skippedNames = append(skippedNames, agentcontract.TaskEventLLMCall)
	}
	return skippedNames
}

func NewToolSelector(decisionModel model.DecisionModel) agentcontract.ToolSelector {
	return acpagent.NewToolSelector(decisionModel)
}
