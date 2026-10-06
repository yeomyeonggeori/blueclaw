package app

import (
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func newTaskLauncher(runtimeConfiguration config.RuntimeConfiguration, foundation runtimeFoundation, directory identityDirectory, kernel agentKernel, services taskServices, toolCatalogBuilder *agentruntime.ToolCatalogBuilder) *agentruntime.TaskLauncher {
	taskLauncher := agentruntime.NewTaskLauncher(kernel.harness, services.taskRunService, toolCatalogBuilder)
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(services.taskRunService, kernel.taskTierLanguageModels.High))
	taskLauncher.UseRequesterWorkspaceProvisioner(security.NewPOSIXRequesterWorkspaceProvisioner(foundation.posixSynchronizer))
	taskLauncher.UseRequesterEmailResolver(directory.identityService)
	taskLauncher.UseAgentIdentityProvider(kernel.agentIdentityProvider)
	taskLauncher.UseCompanyProvider(directory.companyProvider)
	toolCatalogBuilder.UseCompanyProvider(directory.companyProvider)
	taskLauncher.UseApprovalGate(kernel.toolCatalog.approvalGate)
	kernel.toolCatalog.approvalGate.UseApprovedCallScheduler(approvedCallScheduler{repository: services.repositories.schedule, companyProvider: directory.companyProvider})
	return taskLauncher
}
