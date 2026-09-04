package app

import (
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func deriveIntakeOptions(runtimeConfiguration config.RuntimeConfiguration) agentcontract.IntakeOptions {
	return agentcontract.IntakeOptions{
		IsEnabled:           runtimeConfiguration.Agent.Intake.Enabled,
		DefaultTaskLevel:    agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel),
		SkillTaskLevelFloor: agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.SkillTaskLevelFloor),
	}
}

func intakeBudgetForConfiguration(runtimeConfiguration config.RuntimeConfiguration) agentruntime.IntakeBudget {
	taskLevelProfile := agentcontract.TaskLevelProfileForLevel(agentcontract.NormalizeTaskLevel(runtimeConfiguration.Agent.DefaultTaskLevel))
	return agentruntime.IntakeBudget{
		TaskLevel:         string(taskLevelProfile.TaskLevel),
		MaxIterationCount: taskLevelProfile.MaxIterationCount,
		MaxToolCallCount:  taskLevelProfile.MaxToolCallCount,
		MaxElapsedSecond:  int(taskLevelProfile.Duration.Seconds()),
	}
}
