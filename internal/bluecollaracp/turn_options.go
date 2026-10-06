package bluecollaracp

import (
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func turnOptionsOf(runtimeConfiguration config.RuntimeConfiguration) agentcontract.TurnOptions {
	taskLevelProfile := agentcontract.TaskLevelProfileForLevel(agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel))
	recoveryBudget := runtimeConfiguration.Agent.FailureRecovery.RecoveryBudget
	return agentcontract.TurnOptions{
		MaxIterationCount:   taskLevelProfile.MaxIterationCount,
		MaxToolCallCount:    taskLevelProfile.MaxToolCallCount,
		MaxElapsedSecond:    int(taskLevelProfile.Duration.Seconds()),
		ContextWindowTokens: runtimeConfiguration.LanguageModel.ContextWindowTokens,
		TaskLevel:           taskLevelProfile.TaskLevel,
		GenerationOptions: model.GenerationOptions{
			Seed:        runtimeConfiguration.Agent.GenerationOptions.Seed,
			Temperature: runtimeConfiguration.Agent.GenerationOptions.Temperature,
		},
		RecoveryBudget: agentcontract.RecoveryBudget{
			CorrectedRetry: recoveryBudget.CorrectedRetry,
			AlternateRoute: recoveryBudget.AlternateRoute,
			AdjacentTool:   recoveryBudget.AdjacentTool,
			NoToolFallback: recoveryBudget.NoToolFallback,
		},
	}
}

func intakeOptionsOf(runtimeConfiguration config.RuntimeConfiguration) agentcontract.IntakeOptions {
	return agentcontract.IntakeOptions{
		IsEnabled:           runtimeConfiguration.Agent.Intake.Enabled,
		DefaultTaskLevel:    agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel),
		SkillTaskLevelFloor: agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.SkillTaskLevelFloor),
	}
}
