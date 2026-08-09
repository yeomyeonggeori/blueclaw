package bluecollarharness

import (
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func New(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
	agentKernel := loop.NewAgentKernel(dependencies.TaskRunStore, dependencies.TaskStepStore)
	agentKernel.UseTaskArtifactService(dependencies.TaskArtifactStore)
	agentKernel.UseTurnOptions(turnOptionsWithOverrides(deriveTurnOptions(dependencies.RuntimeConfiguration), dependencies.TurnOptionOverrides))
	agentKernel.UseIntakeOptions(intakeOptionsOrDerived(dependencies))
	agentKernel.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
	taskTierLanguageModels := dependencies.TaskTierLanguageModels
	if taskTierLanguageModels.Low != nil {
		agentKernel.UseLanguageModelProvider(taskTierLanguageModels.Low)
		agentKernel.UseTaskTierLanguageModels(taskTierLanguageModels)
	}
	skillRetriever := loop.NewEmbeddingSkillRetriever(dependencies.EmbeddingProvider, dependencies.SkillIndexPath)
	skillRetriever.EmbeddingModel = dependencies.EmbeddingModelName
	agentKernel.UseSkillRetriever(skillRetriever)
	if dependencies.CompanyProvider != nil {
		agentKernel.UseCompanyProvider(dependencies.CompanyProvider)
	}
	if dependencies.IntakeLanguageModelProvider != nil {
		agentKernel.UseIntakeLanguageModelProvider(dependencies.IntakeLanguageModelProvider)
	}
	return agentKernel, skillRetriever
}

func deriveTurnOptions(runtimeConfiguration config.RuntimeConfiguration) agentcontract.TurnOptions {
	taskLevelProfile := loop.TaskLevelProfileForLevel(agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel))
	return agentcontract.TurnOptions{
		MaxIterationCount:   taskLevelProfile.MaxIterationCount,
		MaxToolCallCount:    taskLevelProfile.MaxToolCallCount,
		MaxElapsedSecond:    int(taskLevelProfile.Duration.Seconds()),
		ContextWindowTokens: runtimeConfiguration.LanguageModel.Capability.ContextWindowTokens,
		TaskLevel:           taskLevelProfile.TaskLevel,
		GenerationOptions: llm.GenerationOptions{
			Seed:        runtimeConfiguration.Agent.GenerationOptions.Seed,
			Temperature: runtimeConfiguration.Agent.GenerationOptions.Temperature,
		},
		RecoveryBudget: agentcontract.RecoveryBudget{
			CorrectedRetry: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.CorrectedRetry,
			AlternateRoute: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.AlternateRoute,
			AdjacentTool:   runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.AdjacentTool,
			NoToolFallback: runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget.NoToolFallback,
		},
	}
}

func deriveIntakeOptions(runtimeConfiguration config.RuntimeConfiguration) agentcontract.IntakeOptions {
	return agentcontract.IntakeOptions{
		IsEnabled:           runtimeConfiguration.Agent.Intake.Enabled,
		DefaultTaskLevel:    agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel),
		SkillTaskLevelFloor: agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.SkillTaskLevelFloor),
	}
}

func intakeOptionsOrDerived(dependencies harnessdriver.Dependencies) agentcontract.IntakeOptions {
	if dependencies.IntakeOptions != nil {
		return *dependencies.IntakeOptions
	}
	return deriveIntakeOptions(dependencies.RuntimeConfiguration)
}

func turnOptionsWithOverrides(turnOptions agentcontract.TurnOptions, overrides agentcontract.TurnOptions) agentcontract.TurnOptions {
	if overrides.TaskLevel != "" {
		taskLevelProfile := loop.TaskLevelProfileForLevel(overrides.TaskLevel)
		turnOptions.TaskLevel = taskLevelProfile.TaskLevel
		turnOptions.MaxIterationCount = taskLevelProfile.MaxIterationCount
		turnOptions.MaxToolCallCount = taskLevelProfile.MaxToolCallCount
		turnOptions.MaxElapsedSecond = int(taskLevelProfile.Duration.Seconds())
	}
	if overrides.MaxIterationCount > 0 {
		turnOptions.MaxIterationCount = overrides.MaxIterationCount
	}
	if overrides.MaxToolCallCount > 0 {
		turnOptions.MaxToolCallCount = overrides.MaxToolCallCount
	}
	if overrides.MaxElapsedSecond > 0 {
		turnOptions.MaxElapsedSecond = overrides.MaxElapsedSecond
	}
	if overrides.RecoveryAttemptLimit != 0 {
		turnOptions.RecoveryAttemptLimit = overrides.RecoveryAttemptLimit
	}
	if overrides.RecoveryBudget != (agentcontract.RecoveryBudget{}) {
		turnOptions.RecoveryBudget = overrides.RecoveryBudget
	}
	return turnOptions
}
