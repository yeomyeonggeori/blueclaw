package app

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/auth"
	"github.com/yeomyeonggeori/blueclaw/internal/backup"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	apiconnector "github.com/yeomyeonggeori/blueclaw/internal/connectors/api"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessselection"
	"github.com/yeomyeonggeori/blueclaw/internal/httpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
	"github.com/yeomyeonggeori/blueclaw/internal/reply"
	runtimelogging "github.com/yeomyeonggeori/blueclaw/internal/runtime"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/scheduler"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/sessionquery"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/userapi"
	capabilitycatalog "github.com/yeomyeonggeori/blueclaw/protocol/generated"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
)

const databaseInitializationTimeout = 240 * time.Second

const backgroundLoopStopGrace = 10 * time.Second

type Application struct {
	httpServer                    *http.Server
	connectorRuntime              *connectors.ConnectorRuntime
	connectorTransports           []connectors.ConnectorTransport
	taskRunService                *task.TaskRunService
	interruptedTaskResumer        interruptedTaskResumer
	runtimeLogger                 *runtimelogging.PersistentLogger
	terminalService               *security.ShellService
	backgroundLoops               sync.WaitGroup
	database                      postgres.Database
	startupError                  error
	connectorRuntimeCancel        context.CancelFunc
	connectorTransportCancel      context.CancelFunc
	interruptedTaskResumeCancel   context.CancelFunc
	taskScheduleCancel            context.CancelFunc
	logRetentionCancel            context.CancelFunc
	memoryUpdateCancel            context.CancelFunc
	taskRetentionCancel           context.CancelFunc
	staleTaskCancel               context.CancelFunc
	taskSchedulePoller            *scheduler.TaskSchedulePoller
	taskRetentionSweeper          *scheduler.TaskRetentionSweeper
	memoryUpdateQueue             *memory.BackgroundMemoryUpdateQueue
	taskSchedulePollSecond        int
	taskRetentionIntervalMinute   int
	interruptedTaskResumeDelay    time.Duration
	languageModelDefaultProvider  string
	languageModelFallbackProvider string
	languageModelConfigured       bool
	protocolIdentityChecker       protocolidentity.Checker
	protocolIdentityExpected      protocolidentity.Identity
	protocolIdentityStatus        *protocolidentity.Result
	protocolIdentityCheckOnce     sync.Once
	protocolIdentityCheckError    error
	refreshSkillIndex             func(context.Context)
	mcpRegistry                   mcpRegistryCloser
}

type mcpRegistryCloser interface {
	Close() error
}

type interruptedTaskResumer interface {
	CanResumeInterruptedTaskRun(task.TaskRun) bool
	ResumeInterruptedTaskRun(context.Context, task.TaskRun) (connectors.ConnectorRuntimeResult, error)
	FailUnresumedInterruptedTaskRun(context.Context, task.TaskRun, string) bool
}

func NewApplication(runtimeConfiguration config.RuntimeConfiguration, policyPath string, agentHarnessFactory harnessdriver.Factory) *Application {
	runtimeLogger, startupError := runtimelogging.NewPersistentLogger(runtimeConfiguration, time.Now())
	if startupError != nil {
		runtimeLogger = runtimelogging.NewDiscardLogger()
	}
	logger := runtimeLogger.Logger
	logger.Info("application.initializing", "stage", "open_database")
	database, databaseError := openRuntimeDatabase(runtimeConfiguration, logger)
	if databaseError != nil && startupError == nil {
		startupError = databaseError
	}
	logger.Info("application.initializing", "stage", "load_policy")
	policyLoader := policy.PolicyLoader{}
	policyDocument, _ := policyLoader.LoadPolicyDocument(policyPath)
	logger.Info("application.initializing", "stage", "posix_synchronize")
	posixSynchronizer := security.NewPOSIXSynchronizer(runtimeConfiguration.Terminal, policyPath)
	if errorValue := posixSynchronizer.Synchronize(context.Background()); errorValue != nil && startupError == nil {
		startupError = errorValue
	}
	logger.Info("application.initializing", "stage", "capability_socket_invariant")
	capabilitySocketInvariantResult, capabilitySocketInvariantError := security.EnsureCapabilitySocketInvariant(runtimeConfiguration.Capabilities.UnixSocketPath, policyDocument)
	if capabilitySocketInvariantError != nil && startupError == nil {
		startupError = capabilitySocketInvariantError
	}
	if capabilitySocketInvariantResult.Skipped {
		logger.Info("application.capability_socket_invariant.skipped", "reason", capabilitySocketInvariantResult.SkipReason)
	} else if capabilitySocketInvariantError == nil {
		logger.Info("application.capability_socket_invariant.passed", "socketPath", capabilitySocketInvariantResult.SocketPath, "group", capabilitySocketInvariantResult.GroupName)
	}
	logger.Info("application.initializing", "stage", "project_policy")
	if database.SQL != nil {
		_ = postgres.NewPersonRepository(database).UpsertPeople(policyDocument)
	}
	logger.Info("application.initializing", "stage", "identity")
	policyProjectionService := policy.PolicyProjectionService{}
	identityService := identity.NewIdentityService(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
	var platformAccountLister adminapi.PlatformAccountLister
	if database.SQL != nil {
		platformAccountRepository := postgres.NewPlatformAccountRepository(database)
		identityService.UsePlatformAccountRepository(platformAccountRepository)
		platformAccountLister = platformAccountRepository
	}
	policyWatcher := &policy.PolicyWatcher{}
	companyProvider := func() agentcontract.CompanyContext {
		company := policyWatcher.CurrentPolicyDocument().Company
		return agentcontract.CompanyContext{
			Name:           company.Name,
			BrandName:      company.BrandName,
			Slogan:         company.Slogan,
			Description:    company.Description,
			Representative: company.Representative,
			Website:        company.Website,
			TimeZone:       company.TimeZone,
		}
	}
	policyWatcher.ReloadPolicyDocument(policyDocument)

	auditHandler := adminapi.NewAuditHandler()
	taskEventService := task.NewTaskEventService()
	taskStepService := task.NewTaskStepService()
	taskArtifactService := task.NewTaskArtifactService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskTemporaryDirectoryReclaimer := task.NewTaskTemporaryDirectoryReclaimer(runtimeConfiguration.Terminal.WorkspaceRootPath, logger)
	taskRunService.RegisterTaskRunTransitionObserver(taskTemporaryDirectoryReclaimer.Observe)
	var taskScheduleRepository task.TaskScheduleRepository
	var taskScheduleSummaryRepository adminapi.TaskScheduleSummaryRepository
	var taskScheduleListRepository adminapi.TaskScheduleListRepository
	var taskScheduleCreatorRepairRepository adminapi.TaskScheduleCreatorRepairRepository
	var connectorEventDiagnosticRepository adminapi.ConnectorEventDiagnosticRepository
	var conversationResetRepository adminapi.ConversationResetRepository
	var taskWaitTokenRepository task.TaskWaitTokenRepository
	var scheduledDeliveryRepository scheduler.TaskScheduleDeliveryRepository
	var personRepository postgres.PersonRepository
	var personReferenceCanonicalizer adminapi.PersonReferenceCanonicalizer
	if database.SQL != nil {
		personRepository = postgres.NewPersonRepository(database)
		personReferenceCanonicalizer = personRepository
		taskEventService.UseRepository(postgres.NewTaskEventRepository(database))
		taskStepService.UseRepository(postgres.NewTaskStepRepository(database))
		taskArtifactService.UseRepository(postgres.NewTaskArtifactRepository(database))
		taskRunService.UseRepository(postgres.NewTaskRunRepository(database))
		taskRunService.InterruptOrphanedRuntimeTaskRuns(task.TaskInterruptReasonRuntimeRestart)
		postgresTaskScheduleRepository := postgres.NewTaskScheduleRepository(database)
		taskScheduleRepository = postgresTaskScheduleRepository
		taskScheduleSummaryRepository = postgresTaskScheduleRepository
		taskScheduleListRepository = postgresTaskScheduleRepository
		taskScheduleCreatorRepairRepository = postgresTaskScheduleRepository
		task.SweepEmptyTaskScheduleTimeZone(postgresTaskScheduleRepository, companyProvider().TimeZone, logger)
		connectorEventDiagnosticRepository = postgres.NewRawEventRepository(database)
		conversationResetRepository = postgres.NewConversationResetRepository(database)
		taskWaitTokenRepository = postgres.NewTaskWaitTokenRepository(database)
		scheduledDeliveryRepository = postgres.NewRawEventRepository(database)
	}
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	taskAuthService := task.NewTaskAuthService(magicLinkService, sessionService, taskRunService)
	logger.Info("application.initializing", "stage", "agent_kernel")
	startupInstructions := loadAgentInstructions(runtimeConfiguration)
	logSkillsMissingTheirTools(logger, startupInstructions.UnavailableSkills)
	logRejectedPersonaDocuments(logger, startupInstructions.RejectedDocuments)
	instructionBundleLoader := func() agentcontract.InstructionBundle {
		return loadAgentInstructionBundle(runtimeConfiguration)
	}
	agentIdentityProvider := func() agentcontract.AgentIdentity {
		return loadAgentIdentity(runtimeConfiguration)
	}
	languageModelRuntimeConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	taskTierLanguageModels := resolveTaskTierLanguageModelProviders(runtimeConfiguration, logger)
	languageModelProvider := taskTierLanguageModels.High
	lowTierLanguageModelProvider := taskTierLanguageModels.Low
	capabilityClient := newCapabilityClient(runtimeConfiguration)
	logger.Info("application.initializing", "stage", "skill_retriever")
	embeddingClient := llm.CapabilityEmbeddingClient{
		CapabilityClient: capabilityClient,
		ModelName:        llm.DefaultEmbeddingModelName,
		ExecutionMode:    firstNonEmptyString(runtimeConfiguration.LanguageModel.Capability.ExecutionMode, "auto"),
	}
	intakeLanguageModelProvider := resolveIntakeLanguageModelProvider(runtimeConfiguration, logger)
	terminalService := security.NewShellService(runtimeConfiguration.Terminal)
	toolCatalogResolver := mcpserver.NewSessionTokenRequesterResolver(newToolCatalogSessionToken)
	toolCatalogHandler := mcpserver.NewToolCatalogHandler(toolCatalogResolver, "1")
	toolCatalogApprovalGate := approvalgate.New(taskRunService)
	toolCatalogApprovalGate.UseLanguageModel(languageModelProvider)
	toolCatalogApprovalGate.UseApprovalTargetResolver(agentruntime.NewCapabilityApprovalTargetResolver(capabilityClient))
	selectedHarnessFactory, harnessSelectionError := harnessselection.Select(runtimeConfiguration.Agent.Harness, agentHarnessFactory, harnessselection.ToolCatalogEndpoint{
		URL:               toolCatalogURL(runtimeConfiguration),
		Resolver:          toolCatalogResolver,
		Handler:           toolCatalogHandler,
		ApprovalGate:      toolCatalogApprovalGate,
		BridgeCommandPath: currentExecutablePath(),
	}, harnessselection.SandboxProcessBoundary{
		Runner:            terminalService.WorkspaceActorFactory(),
		WorkspaceRootPath: runtimeConfiguration.Terminal.WorkspaceRootPath,
	})
	selectedHarnessName := strings.TrimSpace(runtimeConfiguration.Agent.Harness.Name)
	if selectedHarnessName == "" {
		selectedHarnessName = harnessselection.BundledHarnessName
	}
	if harnessSelectionError != nil {
		selectedHarnessName = "unavailable"
		logger.Error("application.harness.unavailable", "error", harnessSelectionError)
		if startupError == nil {
			startupError = harnessSelectionError
		}
		selectedHarnessFactory = agentHarnessFactory
	}
	if selectedHarnessFactory == nil {
		selectedHarnessFactory = func(harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
			return nil, nil
		}
	}
	harness, skillRetriever := selectedHarnessFactory(harnessdriver.Dependencies{
		RuntimeConfiguration: runtimeConfiguration,
		TaskRunStore:         taskRunService,
		TaskStepStore:        taskStepService,
		TaskArtifactStore:    taskArtifactService,
		ToolResultSpillStore: agentruntime.NewRequesterToolResultSpillStore(terminalService.WorkspaceActorFactory(), taskRunService),
		ToolResultImageSource: agentruntime.NewRequesterToolResultImageSource(
			terminalService.WorkspaceActorFactory(),
			taskRunService,
			runtimeConfiguration.Terminal.WorkspaceRootPath,
		),
		InstructionBundleLoader:     instructionBundleLoader,
		CompanyProvider:             companyProvider,
		EmbeddingProvider:           embeddingClient,
		EmbeddingModelName:          embeddingClient.ModelName,
		SkillIndexPath:              skillIndexPath(runtimeConfiguration),
		TaskTierLanguageModels:      taskTierLanguageModels,
		IntakeLanguageModelProvider: intakeLanguageModelProvider,
	})
	refreshSkillIndex := func(ctx context.Context) {
		if skillRetriever == nil {
			return
		}
		skillRetriever.Refresh(ctx, instructionBundleLoader().Skills)
	}
	logger.Info("application.initializing", "stage", "memory")
	memoryService := &memory.MemoryService{}
	if strings.TrimSpace(runtimeConfiguration.Memory.GraphitiEndpoint) != "" {
		memoryService.UseGraphStore(memory.NewGraphitiClient(
			runtimeConfiguration.Memory.GraphitiEndpoint,
			time.Duration(runtimeConfiguration.Memory.TimeoutSecond)*time.Second,
		))
	} else {
		logger.Info("application.memory.graph_store_not_configured")
	}
	var memoryGraphReporter memory.GraphMemoryReporter
	var memoryGraphMigrator memory.GraphMemoryMigrator
	if database.SQL != nil {
		graphitiMemoryRepository := postgres.NewGraphitiMemoryRepository(database)
		memoryService.UseMirror(graphitiMemoryRepository)
		memoryGraphReporter = graphitiMemoryRepository
		memoryGraphMigrator = graphitiMemoryRepository
	}
	pinnedMemoryStore := memory.NewMarkdownStore(pinnedMemoryRootPath(runtimeConfiguration), pinnedMemoryHardLimitCharacterCount(runtimeConfiguration))
	pinnedMemoryStore.UseCompressor(memory.NewLLMMarkdownMemoryCompressor(lowTierLanguageModelProvider), pinnedMemoryCompressionTargetCharacterCount(runtimeConfiguration))
	memoryUpdateProcessor := memory.NewMemoryUpdateProcessor(memoryService, pinnedMemoryStore)
	memoryUpdateQueue := memory.NewBackgroundMemoryUpdateQueue(memoryUpdateProcessor, logger)
	backupCoordinator := backup.NewCoordinator(buildBackupManifest(runtimeConfiguration, database))
	taskIntakeController := runtimecontrol.NewTaskIntakeController()
	mcpRegistry := mcp.NewMcpRegistry()
	mcpLoadReport := mcpRegistry.LoadServerDefinition(runtimeConfiguration.MCPServers)
	logMCPServerQuarantines(logger, mcpLoadReport)
	logger.Info("application.initializing", "stage", "tool_catalog")
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMCPRegistry(mcpRegistry)
	toolCatalogBuilder.UseMCPQuarantineReporter(func(quarantinedProvider toolcontract.QuarantinedToolProvider) {
		logMCPProviderQuarantine(logger, quarantinedProvider)
	})
	toolCatalogBuilder.UseCapabilityQuarantineReporter(func(quarantinedProvider toolcontract.QuarantinedToolProvider) {
		logCapabilityProviderQuarantine(logger, quarantinedProvider)
	})
	toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, capabilityToolDescriptors(runtimeConfiguration.Capabilities.ToolDescriptors))
	seedCompanionStatus(capabilityClient, toolCatalogBuilder)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	toolCatalogBuilder.UseSkillSearch(skillRetriever, instructionBundleLoader)
	toolCatalogBuilder.UseTerminalService(terminalService)
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseTaskArtifactService(taskArtifactService)
	toolCatalogBuilder.UseTaskScheduleRepository(taskScheduleRepository)
	toolCatalogBuilder.UseTaskWaitTokenRepository(taskWaitTokenRepository)
	toolCatalogBuilder.UseWorkspaceRootPath(runtimeConfiguration.Terminal.WorkspaceRootPath)
	toolCatalogBuilder.UseOptionalFileReadPathSuffixes(runtimeConfiguration.Agent.OptionalFileReadPathSuffixes)
	toolCatalogBuilder.UseSkillChangeHandler(refreshSkillIndex)
	toolCatalogBuilder.UseMemoryService(memoryService)
	toolCatalogBuilder.UsePinnedMemoryStore(pinnedMemoryStore)
	toolCatalogBuilder.UseMemoryUpdateQueue(memoryUpdateQueue)
	turnRouter := intake.NewTurnRouter(turnRouterLanguageModelProvider(taskTierLanguageModels, intakeLanguageModelProvider), deriveIntakeOptions(runtimeConfiguration))
	taskLauncher := agentruntime.NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(turnRouter)
	taskLauncher.UseIntakeBudget(intakeBudgetForConfiguration(runtimeConfiguration))
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, languageModelProvider))
	taskLauncher.UseRequesterWorkspaceProvisioner(security.NewPOSIXRequesterWorkspaceProvisioner(posixSynchronizer))
	taskLauncher.UseRequesterEmailResolver(identityService)
	taskLauncher.UseAgentIdentityProvider(agentIdentityProvider)
	taskLauncher.UseCompanyProvider(companyProvider)
	toolCatalogBuilder.UseCompanyProvider(companyProvider)
	taskLauncher.UseApprovalGate(toolCatalogApprovalGate)
	var taskSchedulePoller *scheduler.TaskSchedulePoller
	if taskScheduleRepository != nil && scheduledDeliveryRepository != nil {
		poller := scheduler.TaskSchedulePoller{
			TaskScheduleRepository: taskScheduleRepository,
			DeliveryRepository:     scheduledDeliveryRepository,
			TaskScheduleRunner:     agentruntime.NewTaskScheduleRunner(taskLauncher),
			TaskRunService:         taskRunService,
			PersonAccessResolver:   identityService,
			TaskIntakeGate:         taskIntakeController,
			WorkspaceID:            runtimeConfiguration.Memory.WorkspaceID,
			WorkerID:               "blueclaw-app",
			Logger:                 logger,
		}
		taskSchedulePoller = &poller
	}
	logger.Info("application.initializing", "stage", "connector_runtime")
	taskRetentionSweeper := &scheduler.TaskRetentionSweeper{
		TaskRunService:      taskRunService,
		TaskEventService:    taskEventService,
		TaskStepService:     taskStepService,
		TaskArtifactService: taskArtifactService,
		Logger:              logger,
		RetentionDays:       runtimeConfiguration.Scheduler.TaskRetentionDays,
	}
	connectorRuntime := connectors.NewConnectorRuntime(
		identityService,
		harness,
		taskRunService,
		taskEventService,
		logger,
	)
	launchFailureCompleter := launchfailure.NewCompleter(taskRunService, languageModelProvider)
	connectorRuntime.UseUnknownAccountResolver(connectors.NewCapabilityUnknownAccountResolver(capabilityClient))
	connectorRuntime.UseLaunchFailureCompleter(launchFailureCompleter)
	replyGenerator := reply.NewGenerator(languageModelProvider, instructionBundleLoader)
	replyGenerator.UseAgentIdentityProvider(agentIdentityProvider)
	replyGenerator.UseCompanyProvider(companyProvider)
	connectorRuntime.UseReplyGenerator(replyGenerator)
	connectorRuntime.UseCompanyProvider(companyProvider)
	connectorRuntime.UseTurnRouter(turnRouter)
	connectorRuntime.UseIntakeClassifier(intake.NewClassifier(classificationLanguageModelProvider(taskTierLanguageModels, intakeLanguageModelProvider)))
	connectorRuntime.UseTaskLauncher(taskLauncher)
	connectorRuntime.UseApprovalGate(toolCatalogApprovalGate)
	connectorRuntime.UseAgentIdentityProvider(agentIdentityProvider)
	connectorRuntime.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	connectorRuntime.UseMemoryService(memoryService)
	connectorRuntime.UseWorkspaceID(runtimeConfiguration.Memory.WorkspaceID)
	connectorRuntime.UseAdminTaskLinkBaseURL(runtimeConfiguration.Agent.AdminTaskLinkBaseURL)
	connectorRuntime.UseWorkspaceActorFactory(terminalService.WorkspaceActorFactory())
	connectorRuntime.UseIngressGate(backupCoordinator)
	connectorRuntime.UseTaskIntakeGate(taskIntakeController)
	if database.SQL != nil {
		connectorRuntime.UseEventRepository(postgres.NewRawEventRepository(database))
	}
	chatdClient := newChatdClient(runtimeConfiguration)
	for _, platform := range capabilitycatalog.MessengerPlatformNames() {
		connectorRuntime.RegisterAdapter(connectors.NewChatdPlatformAdapter(platform, chatdClient))
	}
	for _, platform := range platformsChatdServesBeyondTheProtocol(runtimeConfiguration.Connectors.Chatd) {
		logger.Warn("connector.platform.served_beyond_the_protocol", "platform", platform)
		connectorRuntime.RegisterAdapter(connectors.NewChatdPlatformAdapter(platform, chatdClient))
	}
	agentReplyStore := apiconnector.NewPersistentReplyStore(filepath.Join(runtimeConfiguration.Terminal.WorkspaceRootPath, ".blueclaw", "state", "agent-replies.json"))
	connectorRuntime.RegisterAdapter(apiconnector.NewAdapter(identityService, agentReplyStore))
	connectorEventHandler := httpserver.NewConnectorEventHandler(connectorRuntime)

	logger.Info("application.initializing", "stage", "router")
	protocolIdentityExpected := expectedProtocolIdentity(runtimeConfiguration)
	protocolIdentityStatus := &protocolidentity.Result{Expected: protocolIdentityExpected}
	protocolIdentityChecker := protocolidentity.NewChecker(protocolidentity.Configuration{
		CapabilityEndpoint:   runtimeConfiguration.Capabilities.Endpoint,
		Timeout:              time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
		CapabilityHTTPClient: capabilityClient.HTTPClient,
	})
	router := httpserver.NewRouter(httpserver.RouterDependencies{
		HealthHandler: httpserver.HealthHandler{
			Database:                 database,
			ConnectorRuntime:         connectorRuntime,
			MemoryService:            memoryService,
			MaximumBacklog:           1000,
			ProtocolIdentity:         protocolIdentityStatus,
			ProtocolIdentityChecker:  &protocolIdentityChecker,
			ProtocolIdentityExpected: protocolIdentityExpected,
		},
		WorkspaceFilesHandler: httpserver.WorkspaceFilesHandler{
			WorkspaceRootPath:     runtimeConfiguration.Terminal.WorkspaceRootPath,
			WorkspaceActorFactory: terminalService.WorkspaceActorFactory(),
			PersonAccessResolver:  identityService,
		},
		PersonaHandler: httpserver.PersonaHandler{
			WorkspaceRootPath:     runtimeConfiguration.Terminal.WorkspaceRootPath,
			WorkspaceActorFactory: terminalService.WorkspaceActorFactory(),
			PersonAccessResolver:  identityService,
		},
		ToolCatalogHandler: toolCatalogHandler,
		PolicyHandler: adminapi.PolicyHandler{
			PolicyPath:                   policyPath,
			PolicyLoader:                 policyLoader,
			PolicySaver:                  policy.PolicySaver{},
			PolicyWatcher:                policyWatcher,
			Validator:                    policy.PolicyValidator{},
			AuditHandler:                 auditHandler,
			PersonReferenceCanonicalizer: personReferenceCanonicalizer,
			PlatformAccountLinker:        identityService,
			OnPolicyReload: func(policyDocument policy.PolicyDocument) {
				if database.SQL != nil {
					_ = personRepository.UpsertPeople(policyDocument)
				}
				identityService.ReloadPolicyProjection(policyProjectionService.ReplacePolicyProjectionTransactionally(policyDocument))
				_ = posixSynchronizer.Synchronize(context.Background())
			},
		},
		IdentityResolve: adminapi.IdentityResolveHandler{
			PolicyWatcher:         policyWatcher,
			PlatformAccountLister: platformAccountLister,
		},
		AuditHandler: auditHandler,
		AttentionHandler: adminapi.AttentionHandler{
			LanguageModel: languageModelProvider,
		},
		TaskMonitorHandler: adminapi.TaskMonitorHandler{
			TaskRunService:   taskRunService,
			TaskStepService:  taskStepService,
			TaskEventService: taskEventService,
			IdentityService:  identityService,
		},
		TaskSearchHandler: adminapi.TaskSearchHandler{SessionQuery: sessionquery.New(taskRunService)},
		TaskRunHandler: adminapi.TaskRunHandler{
			TaskLauncher:            taskLauncher,
			IdentityService:         identityService,
			WorkspaceID:             runtimeConfiguration.Memory.WorkspaceID,
			TaskRunService:          taskRunService,
			TaskIntakeGate:          taskIntakeController,
			AllowTaskDecisionPreset: runtimeConfiguration.Agent.AllowAdminTaskDiagnostic,
		},
		HarnessStatusHandler: adminapi.HarnessStatusHandler{Status: adminapi.HarnessStatus{
			Name:                    selectedHarnessName,
			AgentCommandPath:        runtimeConfiguration.Agent.Harness.AgentCommandPath,
			RunsAsRequesterIdentity: strings.TrimSpace(runtimeConfiguration.Terminal.POSIXHelperPath) != "",
			ToolCatalogURL:          toolCatalogURL(runtimeConfiguration),
		}},
		SkillInventoryHandler: adminapi.SkillInventoryHandler{InventoryLoader: func() adminapi.SkillInventory {
			instructions := loadAgentInstructions(runtimeConfiguration)
			return adminapi.SkillInventory{Loaded: instructions.Bundle.Skills, Unavailable: instructions.UnavailableSkills}
		}},
		ToolInventoryHandler: adminapi.ToolInventoryHandler{ToolCatalogBuilder: toolCatalogBuilder},
		TaskApprovalHandler: adminapi.TaskApprovalHandler{
			TaskLauncher:    taskLauncher,
			TaskRunService:  taskRunService,
			IdentityService: identityService,
		},
		QuiesceHandler: adminapi.QuiesceHandler{
			Controller:     taskIntakeController,
			TaskRunService: taskRunService,
		},
		TaskScheduleHandler: adminapi.TaskScheduleHandler{
			CompanyProvider:   companyProvider,
			SummaryRepository: taskScheduleSummaryRepository,
			ListRepository:    taskScheduleListRepository,
			RepairRepository:  taskScheduleCreatorRepairRepository,
		},
		ConnectorDiagnostics: adminapi.ConnectorEventDiagnosticHandler{
			Repository: connectorEventDiagnosticRepository,
		},
		ConversationReset: adminapi.ConversationResetHandler{
			Repository: conversationResetRepository,
		},
		MemoryGraphHandler: adminapi.MemoryGraphHandler{
			MemoryService: memoryService,
			Reporter:      memoryGraphReporter,
			Migrator:      memoryGraphMigrator,
			MarkdownStore: pinnedMemoryStore,
			Identity:      identityService,
		},
		BackupHandler: adminapi.BackupHandler{
			Coordinator: backupCoordinator,
		},
		TaskInboxHandler: userapi.TaskInboxHandler{
			TaskRunService:  taskRunService,
			TaskStepService: taskStepService,
			TaskAuthService: taskAuthService,
		},
		TaskActionHandler: userapi.TaskActionHandler{
			TaskRunService:  taskRunService,
			TaskAuthService: taskAuthService,
		},
		SSEHandler: httpserver.SSEHandler{
			TaskEventService: taskEventService,
		},
		ConnectorEventHandler: connectorEventHandler,
		AgentReplyHandler: httpserver.AgentReplyHandler{
			ReplyStore: agentReplyStore,
		},
	})

	connectorTransports := []connectors.ConnectorTransport{}
	for _, platform := range capabilitycatalog.MessengerPlatformNames() {
		connectorTransports = append(connectorTransports, connectors.NewHTTPWebhookTransport(platform+"-internal-ingress", platform))
	}

	logger.Info("application.initializing", "stage", "ready")
	return &Application{
		httpServer: &http.Server{
			Addr:    deriveListenAddress(runtimeConfiguration.BaseURL),
			Handler: router,
		},
		connectorRuntime:              connectorRuntime,
		connectorTransports:           connectorTransports,
		taskRunService:                taskRunService,
		interruptedTaskResumer:        connectorRuntime,
		runtimeLogger:                 runtimeLogger,
		terminalService:               terminalService,
		database:                      database,
		startupError:                  startupError,
		taskSchedulePoller:            taskSchedulePoller,
		taskRetentionSweeper:          taskRetentionSweeper,
		memoryUpdateQueue:             memoryUpdateQueue,
		taskSchedulePollSecond:        runtimeConfiguration.Scheduler.TaskSchedulePollIntervalSecond,
		taskRetentionIntervalMinute:   runtimeConfiguration.Scheduler.RetentionCheckIntervalMinute,
		interruptedTaskResumeDelay:    2 * time.Second,
		languageModelDefaultProvider:  languageModelRuntimeConfiguration.LanguageModel.DefaultProvider,
		languageModelFallbackProvider: languageModelRuntimeConfiguration.LanguageModel.FallbackProvider,
		languageModelConfigured:       languageModelProvider != nil,
		protocolIdentityChecker:       protocolIdentityChecker,
		protocolIdentityExpected:      protocolIdentityExpected,
		protocolIdentityStatus:        protocolIdentityStatus,
		refreshSkillIndex:             refreshSkillIndex,
		mcpRegistry:                   mcpRegistry,
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
