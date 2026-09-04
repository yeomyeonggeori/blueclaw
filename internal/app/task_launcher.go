package app

import (
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
)

func newTaskLauncher(runtimeConfiguration config.RuntimeConfiguration, foundation runtimeFoundation, directory identityDirectory, kernel agentKernel, services taskServices, toolCatalogBuilder *agentruntime.ToolCatalogBuilder, turnRouter intake.TurnRouter) *agentruntime.TaskLauncher {
	taskLauncher := agentruntime.NewTaskLauncher(kernel.harness, services.taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(turnRouter)
	taskLauncher.UseIntakeBudget(intakeBudgetForConfiguration(runtimeConfiguration))
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(services.taskRunService, kernel.taskTierLanguageModels.High))
	taskLauncher.UseRequesterWorkspaceProvisioner(security.NewPOSIXRequesterWorkspaceProvisioner(foundation.posixSynchronizer))
	taskLauncher.UseRequesterEmailResolver(directory.identityService)
	taskLauncher.UseAgentIdentityProvider(kernel.agentIdentityProvider)
	taskLauncher.UseCompanyProvider(directory.companyProvider)
	toolCatalogBuilder.UseCompanyProvider(directory.companyProvider)
	taskLauncher.UseApprovalGate(kernel.toolCatalog.approvalGate)
	return taskLauncher
}

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
