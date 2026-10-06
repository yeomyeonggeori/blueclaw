package bluecollaracp

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func configuredRuntime() config.RuntimeConfiguration {
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.LanguageModel.ContextWindowTokens = 128000
	seed := int64(41)
	temperature := 0.2
	runtimeConfiguration.Agent.GenerationOptions.Seed = &seed
	runtimeConfiguration.Agent.GenerationOptions.Temperature = &temperature
	runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.CorrectedRetry = 3
	runtimeConfiguration.Agent.DefaultTaskLevel = "high"
	runtimeConfiguration.Agent.SkillTaskLevelFloor = "medium"
	return runtimeConfiguration
}

func TestTheConfiguredContextWindowAndGenerationOptionsReachTheAgent(t *testing.T) {
	process := agentProcess{dependencies: harnessdriver.Dependencies{RuntimeConfiguration: configuredRuntime()}}

	options := process.optionsFor(agentcontract.AgentTurnRequest{ToolSet: toolSetWithOneGatedTool(t)})

	turnOptions := options.TurnOptions
	if turnOptions.ContextWindowTokens != 128000 {
		t.Fatalf("expected the configured context window, got %d", turnOptions.ContextWindowTokens)
	}
	if turnOptions.GenerationOptions.Seed == nil || *turnOptions.GenerationOptions.Seed != 41 || turnOptions.GenerationOptions.Temperature == nil || *turnOptions.GenerationOptions.Temperature != 0.2 {
		t.Fatalf("expected the configured seed and temperature, got %+v", turnOptions.GenerationOptions)
	}
	if turnOptions.RecoveryBudget.CorrectedRetry != 3 {
		t.Fatalf("expected the configured recovery budget, got %+v", turnOptions.RecoveryBudget)
	}
}

func TestTheConfiguredTaskLevelsReachTheAgentsRouting(t *testing.T) {
	process := agentProcess{dependencies: harnessdriver.Dependencies{RuntimeConfiguration: configuredRuntime()}}

	options := process.optionsFor(agentcontract.AgentTurnRequest{ToolSet: toolSetWithOneGatedTool(t)})

	if options.IntakeOptions.DefaultTaskLevel != agentcontract.NormalizeTaskLevel("high") || options.IntakeOptions.SkillTaskLevelFloor != agentcontract.NormalizeTaskLevel("medium") {
		t.Fatalf("expected the configured default and floor, got %+v", options.IntakeOptions)
	}
}
