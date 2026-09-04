package app

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/httpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/sessionquery"
	"github.com/yeomyeonggeori/blueclaw/internal/userapi"
)

func newRouterDependencies(components applicationComponents) httpserver.RouterDependencies {
	runtimeConfiguration := components.runtimeConfiguration
	directory := components.directory
	services := components.services
	kernel := components.kernel
	return httpserver.RouterDependencies{
		HealthHandler:         newHealthHandler(components),
		WorkspaceFilesHandler: newWorkspaceFilesHandler(runtimeConfiguration, kernel, directory),
		PersonaHandler:        newPersonaHandler(runtimeConfiguration, kernel, directory),
		ToolCatalogHandler:    kernel.toolCatalog.handler,
		PolicyHandler:         newPolicyHandler(components),
		IdentityResolve:       adminapi.IdentityResolveHandler{PolicyWatcher: directory.policyWatcher, PlatformAccountLister: directory.platformAccountLister},
		AuditHandler:          services.auditHandler,
		AttentionHandler:      adminapi.AttentionHandler{LanguageModel: kernel.taskTierLanguageModels.High},
		TaskMonitorHandler:    newTaskMonitorHandler(services, directory),
		TaskSearchHandler:     adminapi.TaskSearchHandler{SessionQuery: sessionquery.New(services.taskRunService)},
		TaskRunHandler:        newTaskRunHandler(runtimeConfiguration, services, directory, components.taskLauncher, components.taskIntakeController),
		HarnessStatusHandler:  newHarnessStatusHandler(runtimeConfiguration, kernel.harnessName),
		SkillInventoryHandler: newSkillInventoryHandler(runtimeConfiguration, kernel.capabilityRegistry),
		ToolInventoryHandler:  adminapi.ToolInventoryHandler{ToolCatalogBuilder: components.toolCatalogBuilder},
		TaskApprovalHandler:   newTaskApprovalHandler(services, directory, components.taskLauncher),
		QuiesceHandler: adminapi.QuiesceHandler{
			Controller:        components.taskIntakeController,
			TaskRunService:    services.taskRunService,
			MemoryUpdateQueue: components.memory.memoryUpdateQueue,
			Logger:            components.foundation.logger,
		},
		TaskScheduleHandler:   newTaskScheduleHandler(services, directory),
		ConnectorDiagnostics:  adminapi.ConnectorEventDiagnosticHandler{Repository: services.repositories.connectorEventDiagnostic},
		ConversationReset:     adminapi.ConversationResetHandler{Repository: services.repositories.conversationReset},
		MemoryGraphHandler:    newMemoryGraphHandler(components.memory, directory),
		BackupHandler:         adminapi.BackupHandler{Coordinator: components.backupCoordinator},
		TaskInboxHandler:      userapi.TaskInboxHandler{TaskRunService: services.taskRunService, TaskStepService: services.taskStepService, TaskAuthService: services.taskAuthService},
		TaskActionHandler:     userapi.TaskActionHandler{TaskRunService: services.taskRunService, TaskAuthService: services.taskAuthService},
		SSEHandler:            httpserver.SSEHandler{TaskEventService: services.taskEventService},
		ConnectorEventHandler: components.connectorEventHandler,
		AgentReplyHandler:     httpserver.AgentReplyHandler{ReplyStore: components.agentReplyStore},
	}
}

func newHealthHandler(components applicationComponents) httpserver.HealthHandler {
	return httpserver.HealthHandler{
		Database:                 components.foundation.database,
		ConnectorRuntime:         components.connectorRuntime,
		MemoryService:            components.memory.memoryService,
		MaximumBacklog:           1000,
		ProtocolIdentity:         components.protocolIdentity.status,
		ProtocolIdentityChecker:  &components.protocolIdentity.checker,
		ProtocolIdentityExpected: components.protocolIdentity.expected,
	}
}

func newWorkspaceFilesHandler(runtimeConfiguration config.RuntimeConfiguration, kernel agentKernel, directory identityDirectory) httpserver.WorkspaceFilesHandler {
	return httpserver.WorkspaceFilesHandler{
		WorkspaceRootPath:     runtimeConfiguration.Terminal.WorkspaceRootPath,
		WorkspaceActorFactory: kernel.terminalService.WorkspaceActorFactory(),
		PersonAccessResolver:  directory.identityService,
	}
}

func newPersonaHandler(runtimeConfiguration config.RuntimeConfiguration, kernel agentKernel, directory identityDirectory) httpserver.PersonaHandler {
	return httpserver.PersonaHandler{
		WorkspaceRootPath:     runtimeConfiguration.Terminal.WorkspaceRootPath,
		WorkspaceActorFactory: kernel.terminalService.WorkspaceActorFactory(),
		PersonAccessResolver:  directory.identityService,
	}
}

func newPolicyHandler(components applicationComponents) adminapi.PolicyHandler {
	return adminapi.PolicyHandler{
		PolicyPath:                   components.policyPath,
		PolicyLoader:                 components.foundation.policyLoader,
		PolicySaver:                  policy.PolicySaver{},
		PolicyWatcher:                components.directory.policyWatcher,
		Validator:                    policy.PolicyValidator{},
		AuditHandler:                 components.services.auditHandler,
		PersonReferenceCanonicalizer: components.services.repositories.personReferenceCanonicalizer,
		PlatformAccountLinker:        components.directory.identityService,
		OnPolicyReload:               newPolicyReloadHandler(components),
	}
}

func newPolicyReloadHandler(components applicationComponents) func(policy.PolicyDocument) {
	database := components.foundation.database
	personRepository := components.services.repositories.person
	identityService := components.directory.identityService
	policyProjectionService := components.directory.policyProjectionService
	posixSynchronizer := components.foundation.posixSynchronizer
	return func(policyDocument policy.PolicyDocument) {
		if database.SQL != nil {
			_ = personRepository.UpsertPeople(policyDocument)
		}
		identityService.ReloadPolicyProjection(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
		_ = posixSynchronizer.Synchronize(context.Background())
	}
}

func newTaskMonitorHandler(services taskServices, directory identityDirectory) adminapi.TaskMonitorHandler {
	return adminapi.TaskMonitorHandler{
		TaskRunService:   services.taskRunService,
		TaskStepService:  services.taskStepService,
		TaskEventService: services.taskEventService,
		IdentityService:  directory.identityService,
	}
}

func newTaskRunHandler(runtimeConfiguration config.RuntimeConfiguration, services taskServices, directory identityDirectory, taskLauncher *agentruntime.TaskLauncher, taskIntakeController *runtimecontrol.TaskIntakeController) adminapi.TaskRunHandler {
	return adminapi.TaskRunHandler{
		TaskLauncher:            taskLauncher,
		IdentityService:         directory.identityService,
		WorkspaceID:             runtimeConfiguration.Memory.WorkspaceID,
		TaskRunService:          services.taskRunService,
		TaskIntakeGate:          taskIntakeController,
		AllowTaskDecisionPreset: runtimeConfiguration.Agent.AllowAdminTaskDiagnostic,
	}
}

func newHarnessStatusHandler(runtimeConfiguration config.RuntimeConfiguration, harnessName string) adminapi.HarnessStatusHandler {
	return adminapi.HarnessStatusHandler{Status: adminapi.HarnessStatus{
		Name:                    harnessName,
		AgentCommandPath:        runtimeConfiguration.Agent.Harness.AgentCommandPath,
		RunsAsRequesterIdentity: strings.TrimSpace(runtimeConfiguration.Terminal.POSIXHelperPath) != "",
		ToolCatalogURL:          toolCatalogURL(runtimeConfiguration),
	}}
}

func newSkillInventoryHandler(runtimeConfiguration config.RuntimeConfiguration, capabilityRegistry *agentruntime.CapabilityRegistry) adminapi.SkillInventoryHandler {
	return adminapi.SkillInventoryHandler{InventoryLoader: func() adminapi.SkillInventory {
		instructions := loadAgentInstructions(runtimeConfiguration, capabilityRegistry)
		return adminapi.SkillInventory{Loaded: instructions.Bundle.Skills, Unavailable: instructions.UnavailableSkills}
	}}
}

func newTaskApprovalHandler(services taskServices, directory identityDirectory, taskLauncher *agentruntime.TaskLauncher) adminapi.TaskApprovalHandler {
	return adminapi.TaskApprovalHandler{
		TaskLauncher:    taskLauncher,
		TaskRunService:  services.taskRunService,
		IdentityService: directory.identityService,
	}
}

func newTaskScheduleHandler(services taskServices, directory identityDirectory) adminapi.TaskScheduleHandler {
	return adminapi.TaskScheduleHandler{
		CompanyProvider:   directory.companyProvider,
		SummaryRepository: services.repositories.taskScheduleSummary,
		ListRepository:    services.repositories.taskScheduleList,
		RepairRepository:  services.repositories.taskScheduleCreatorRepair,
	}
}

func newMemoryGraphHandler(memoryComponents memoryComponents, directory identityDirectory) adminapi.MemoryGraphHandler {
	return adminapi.MemoryGraphHandler{
		MemoryService: memoryComponents.memoryService,
		Reporter:      memoryComponents.graphReporter,
		Migrator:      memoryComponents.graphMigrator,
		MarkdownStore: memoryComponents.pinnedMemoryStore,
		Identity:      directory.identityService,
	}
}
