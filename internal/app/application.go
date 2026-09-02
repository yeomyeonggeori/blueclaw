package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/adminapi"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/auth"
	"github.com/yeomyeonggeori/blueclaw/internal/backup"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
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
	"github.com/yeomyeonggeori/blueclaw/internal/skill"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/userapi"
	capabilitycatalog "github.com/yeomyeonggeori/blueclaw/protocol/generated"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model"
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
		connectorEventDiagnosticRepository = postgres.NewRawEventRepository(database)
		conversationResetRepository = postgres.NewConversationResetRepository(database)
		taskWaitTokenRepository = postgres.NewTaskWaitTokenRepository(database)
		scheduledDeliveryRepository = postgres.NewRawEventRepository(database)
	}
	magicLinkService := auth.NewMagicLinkService()
	sessionService := auth.NewSessionService()
	taskAuthService := task.NewTaskAuthService(magicLinkService, sessionService, taskRunService)
	logger.Info("application.initializing", "stage", "agent_kernel")
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
	connectorRuntime.RegisterAdapter(newPlatformAdapter("mattermost", runtimeConfiguration, capabilityClient, chatdClient))
	connectorRuntime.RegisterAdapter(newPlatformAdapter("slack", runtimeConfiguration, capabilityClient, chatdClient))
	connectorRuntime.RegisterAdapter(newPlatformAdapter("signal", runtimeConfiguration, capabilityClient, chatdClient))
	agentReplyStore := apiconnector.NewPersistentReplyStore(filepath.Join(runtimeConfiguration.Terminal.WorkspaceRootPath, ".blueclaw", "state", "agent-replies.json"))
	connectorRuntime.RegisterAdapter(apiconnector.NewAdapter(identityService, agentReplyStore))
	connectorRuntime.RegisterAdapter(connectors.NewChatdPlatformAdapter("buzz", chatdClient))
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

	connectorTransports := []connectors.ConnectorTransport{
		connectors.NewHTTPWebhookTransport("mattermost-internal-ingress", "mattermost"),
		connectors.NewHTTPWebhookTransport("slack-internal-ingress", "slack"),
		connectors.NewHTTPWebhookTransport("signal-internal-ingress", "signal"),
		connectors.NewHTTPWebhookTransport("buzz-internal-ingress", "buzz"),
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

func logMCPServerQuarantines(logger *slog.Logger, report mcp.LoadReport) {
	if logger == nil {
		return
	}
	for _, quarantinedServer := range report.Quarantined {
		logger.Warn("mcp.server.quarantined", "serverName", quarantinedServer.Name, "reason", quarantinedServer.Reason)
	}
}

func logMCPProviderQuarantine(logger *slog.Logger, quarantinedProvider toolcontract.QuarantinedToolProvider) {
	if logger == nil {
		return
	}
	logger.Warn("mcp.provider.quarantined", "providerID", quarantinedProvider.ProviderID, "reason", quarantinedProvider.Reason)
}

func logCapabilityProviderQuarantine(logger *slog.Logger, quarantinedProvider toolcontract.QuarantinedToolProvider) {
	if logger == nil {
		return
	}
	logger.Warn("capability.provider.quarantined", "providerID", quarantinedProvider.ProviderID, "reason", quarantinedProvider.Reason)
}

func loadAgentInstructionPrompt(runtimeConfiguration config.RuntimeConfiguration) string {
	return loadAgentInstructionBundle(runtimeConfiguration).Prompt
}

func loadAgentInstructionBundle(runtimeConfiguration config.RuntimeConfiguration) agentcontract.InstructionBundle {
	parts := []string{}
	sources := []agentcontract.InstructionSource{}
	skillInstructions := []agentcontract.SkillInstruction{}
	includedSkillByName := map[string]bool{}
	for _, rootPath := range instructionRootPaths(runtimeConfiguration) {
		for _, instructionDocument := range readInstructionDocuments(rootPath) {
			if instructionDocument.Prompt == "" {
				continue
			}
			parts = append(parts, instructionDocument.Prompt)
			sources = append(sources, instructionDocument.Source)
		}
		if instructionDocument, instructionSource := readLegacyInstructionDocument(rootPath); instructionDocument != "" {
			parts = append(parts, instructionDocument)
			sources = append(sources, instructionSource)
		}
		discoveredSkillInstructions := readSkillInstructions(rootPath, agentruntime.BundledSkillRootPath(rootPath))
		for _, skillInstruction := range discoveredSkillInstructions {
			if strings.TrimSpace(skillInstruction.Name) != "" {
				includedSkillByName[skillInstruction.Name] = true
			}
			skillInstructions = append(skillInstructions, skillInstruction)
		}
	}
	if !includedSkillByName["agent-browser"] {
		sources = append(sources, agentcontract.InstructionSource{
			Path:      ".agents/skills/agent-browser/SKILL.md",
			SkillName: "agent-browser",
			Missing:   true,
		})
	}
	return agentcontract.InstructionBundle{
		Prompt:  strings.Join(parts, "\n\n"),
		Sources: sources,
		Skills:  skillInstructions,
	}
}

func pinnedMemoryRootPath(runtimeConfiguration config.RuntimeConfiguration) string {
	if strings.TrimSpace(runtimeConfiguration.Memory.PinnedMemoryRootPath) != "" {
		return strings.TrimSpace(runtimeConfiguration.Memory.PinnedMemoryRootPath)
	}
	return filepath.Join(runtimeConfiguration.Terminal.WorkspaceRootPath, ".blueclaw", "memory")
}

func pinnedMemoryHardLimitCharacterCount(runtimeConfiguration config.RuntimeConfiguration) int {
	if runtimeConfiguration.Memory.PinnedMemoryHardLimitCharacterCount > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryHardLimitCharacterCount
	}
	if runtimeConfiguration.Memory.PinnedMemoryCharacterLimit > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryCharacterLimit
	}
	return memory.DefaultPinnedMemoryHardLimitCharacterCount
}

func pinnedMemoryCompressionTargetCharacterCount(runtimeConfiguration config.RuntimeConfiguration) int {
	if runtimeConfiguration.Memory.PinnedMemoryCompressionTargetCharacterCount > 0 {
		return runtimeConfiguration.Memory.PinnedMemoryCompressionTargetCharacterCount
	}
	return memory.DefaultPinnedMemoryCompressionTargetCharacterCount
}

func instructionRootPaths(runtimeConfiguration config.RuntimeConfiguration) []string {
	rootPathByPath := map[string]bool{}
	rootPaths := []string{}
	for _, rootPath := range []string{runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace", "."} {
		cleanRootPath := strings.TrimSpace(rootPath)
		if cleanRootPath == "" || rootPathByPath[cleanRootPath] {
			continue
		}
		rootPathByPath[cleanRootPath] = true
		rootPaths = append(rootPaths, cleanRootPath)
	}
	return rootPaths
}

type instructionDocument struct {
	Prompt string
	Source agentcontract.InstructionSource
}

func readInstructionDocuments(rootPath string) []instructionDocument {
	documents := []instructionDocument{}
	for _, fileName := range []string{"IDENTITY.md", "BOT_PROFILE.yaml", "SOUL.md"} {
		path := filepath.Join(rootPath, fileName)
		document, errorValue := os.ReadFile(path)
		if errorValue == nil && strings.TrimSpace(string(document)) != "" {
			prompt := strings.TrimSpace(string(document))
			if fileName == "BOT_PROFILE.yaml" {
				prompt = renderBotProfileInstruction(document)
			}
			documents = append(documents, instructionDocument{
				Prompt: prompt,
				Source: instructionSource(path, "", document),
			})
		}
	}
	return documents
}

func readLegacyInstructionDocument(rootPath string) (string, agentcontract.InstructionSource) {
	for _, fileName := range []string{"AGENTS.md", "CLAUDE.md"} {
		path := filepath.Join(rootPath, fileName)
		document, errorValue := os.ReadFile(path)
		if errorValue == nil && strings.TrimSpace(string(document)) != "" {
			return strings.TrimSpace(string(document)), instructionSource(path, "", document)
		}
	}
	return "", agentcontract.InstructionSource{}
}

func readSkillInstructions(rootPath string, bundledSkillsPath string) []agentcontract.SkillInstruction {
	skillInstructions := []agentcontract.SkillInstruction{}
	skillRegistry := skill.NewSkillRegistry()
	for _, skillRoot := range []string{filepath.Join(rootPath, ".agents", "skills"), bundledSkillsPath} {
		discoveredSkillBundles, errorValue := skillRegistry.DiscoverSkill(skillRoot)
		if errorValue == nil {
			for _, skillBundle := range discoveredSkillBundles {
				document, readError := os.ReadFile(filepath.Join(skillBundle.DirectoryPath, "SKILL.md"))
				if readError == nil {
					skillInstructions = append(skillInstructions, agentcontract.SkillInstruction{
						Name:           skillBundle.Name,
						Description:    skillBundle.Description,
						Prompt:         strings.TrimSpace((skill.SkillPromptBuilder{}).BuildSkillPrompt([]skill.SkillBundle{skillBundle})),
						ToolReferences: skillBundle.ReferencedToolNames(),
						Source:         instructionSource(filepath.Join(skillBundle.DirectoryPath, "SKILL.md"), skillBundle.Name, document),
					})
				}
			}
		}
	}
	return skillInstructions
}

func loadAgentIdentity(runtimeConfiguration config.RuntimeConfiguration) agentcontract.AgentIdentity {
	for _, rootPath := range instructionRootPaths(runtimeConfiguration) {
		document, errorValue := os.ReadFile(filepath.Join(rootPath, "BOT_PROFILE.yaml"))
		if errorValue != nil {
			continue
		}
		profile := parseSimpleYAML(document)
		agentIdentity := agentcontract.AgentIdentity{
			Name:   strings.TrimSpace(profile["displayName"]),
			Handle: strings.TrimSpace(profile["username"]),
		}
		if agentIdentity.Name != "" || agentIdentity.Handle != "" {
			return agentIdentity
		}
	}
	return agentcontract.AgentIdentity{}
}

func renderBotProfileInstruction(document []byte) string {
	profile := parseSimpleYAML(document)
	lines := []string{"Runtime bot profile:"}
	if username := strings.TrimSpace(profile["username"]); username != "" {
		lines = append(lines, "- internal username: "+username)
	}
	lines = append(lines,
		"- current displayName: "+profile["displayName"],
		"- English displayName: "+profile["englishDisplayName"],
		"- aliases: "+profile["aliases"],
		"- public description: "+profile["publicDescription"],
	)
	if strings.TrimSpace(profile["identityExtension"]) != "" {
		lines = append(lines, "Identity extension:\n"+strings.TrimSpace(profile["identityExtension"]))
	}
	return strings.Join(lines, "\n")
}

func parseSimpleYAML(document []byte) map[string]string {
	values := map[string]string{}
	lines := strings.Split(string(document), "\n")
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "#") || line == "---" {
			continue
		}
		if line == "aliases:" {
			aliases := []string{}
			for index+1 < len(lines) {
				nextLine := strings.TrimSpace(lines[index+1])
				if !strings.HasPrefix(nextLine, "- ") {
					break
				}
				aliases = append(aliases, unquoteSimpleYAML(strings.TrimSpace(strings.TrimPrefix(nextLine, "- "))))
				index++
			}
			values["aliases"] = strings.Join(aliases, ", ")
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found {
			values[strings.TrimSpace(key)] = unquoteSimpleYAML(strings.TrimSpace(value))
		}
	}
	return values
}

func unquoteSimpleYAML(value string) string {
	return strings.Trim(value, `"'`)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func instructionSource(path string, skillName string, document []byte) agentcontract.InstructionSource {
	hash := sha256.Sum256(document)
	return agentcontract.InstructionSource{
		Path:      path,
		SkillName: skillName,
		ByteSize:  len(document),
		SHA256:    hex.EncodeToString(hash[:]),
	}
}

func deriveAllowedToolNames(runtimeConfiguration config.RuntimeConfiguration) []string {
	allowedToolNameByName := map[string]bool{}
	for _, toolName := range toolcontract.KernelToolNames() {
		allowedToolNameByName[toolName] = true
	}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		for _, allowedToolName := range agentProfile.AllowedToolNames {
			trimmedToolName := strings.TrimSpace(allowedToolName)
			if toolcontract.IsKernelToolName(trimmedToolName) {
				allowedToolNameByName[trimmedToolName] = true
			}
		}
	}
	allowedToolNames := []string{}
	for allowedToolName := range allowedToolNameByName {
		allowedToolNames = append(allowedToolNames, allowedToolName)
	}
	return allowedToolNames
}

func seedCompanionStatus(capabilityClient capability.Client, toolCatalogBuilder *agentruntime.ToolCatalogBuilder) {
	if strings.TrimSpace(capabilityClient.Endpoint) == "" {
		return
	}
	statusContext, cancelStatus := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStatus()
	var response struct {
		CompanionStatus string `json:"companionStatus"`
	}
	if errorValue := capabilityClient.GetJSON(statusContext, "/v1/capabilities", &response); errorValue != nil {
		return
	}
	toolCatalogBuilder.UseCompanionStatus(response.CompanionStatus)
}

func capabilityToolDescriptors(toolDescriptors []config.CapabilityToolDescriptor) []agentruntime.CapabilityToolDescriptor {
	catalogToolDescriptors := []agentruntime.CapabilityToolDescriptor{}
	for _, toolDescriptor := range toolDescriptors {
		trimmedName := strings.TrimSpace(toolDescriptor.Name)
		if trimmedName == "" {
			continue
		}
		catalogToolDescriptors = append(catalogToolDescriptors, agentruntime.CapabilityToolDescriptor{
			Name:                    trimmedName,
			CanonicalName:           toolDescriptor.CanonicalName,
			Namespace:               toolDescriptor.Namespace,
			ModelName:               toolDescriptor.ModelName,
			ModelVisibility:         toolDescriptor.ModelVisibility,
			Description:             strings.TrimSpace(toolDescriptor.Description),
			PrivacyClass:            toolDescriptor.PrivacyClass,
			RequiresUserPresence:    toolDescriptor.RequiresUserPresence,
			RequiresRequesterDevice: toolDescriptor.RequiresRequesterDevice,
			ApprovalScope:           toolDescriptor.ApprovalScope,
			WorksOffline:            toolDescriptor.WorksOffline,
			InputSchema:             toolDescriptor.InputSchema,
			InputIntentSchema:       toolDescriptor.InputIntentSchema,
			OutputSchema:            toolDescriptor.OutputSchema,
			ResultContract:          capabilityToolResultContract(toolDescriptor.ResultContract),
			PolicyResource:          toolDescriptor.PolicyResource,
			SideEffectClass:         toolDescriptor.SideEffectClass,
			RequiresApproval:        toolDescriptor.RequiresApproval,
			CompletionEvidence:      capabilityCompletionEvidence(toolDescriptor.CompletionEvidence),
			Availability: agentruntime.CapabilityAvailability{
				State:  toolDescriptor.Availability.State,
				Reason: toolDescriptor.Availability.Reason,
			},
			Idempotency: agentruntime.CapabilityIdempotency{
				Supported: toolDescriptor.Idempotency.Supported,
				Required:  toolDescriptor.Idempotency.Required,
				Scope:     toolDescriptor.Idempotency.Scope,
			},
		})
	}
	return catalogToolDescriptors
}

func capabilityToolResultContract(contract *config.CapabilityToolResultContract) *agentruntime.CapabilityToolResultContract {
	if contract == nil {
		return nil
	}
	effects := make([]agentruntime.CapabilityResourceEffectContract, 0, len(contract.Effects))
	for _, effectContract := range contract.Effects {
		effects = append(effects, agentruntime.CapabilityResourceEffectContract{
			ObjectType:     effectContract.ObjectType,
			Effect:         effectContract.Effect,
			ResultField:    effectContract.ResultField,
			EffectIdentity: effectContract.EffectIdentity,
			When:           capabilityEvidenceCondition(effectContract.When),
		})
	}
	return &agentruntime.CapabilityToolResultContract{
		Schema:            contract.Schema,
		Effects:           effects,
		EvidenceCondition: capabilityEvidenceCondition(contract.EvidenceCondition),
	}
}

func capabilityEvidenceCondition(condition *config.EvidenceCondition) *agentruntime.CapabilityEvidenceCondition {
	if condition == nil {
		return nil
	}
	return &agentruntime.CapabilityEvidenceCondition{
		ResultField: condition.ResultField,
		Equals:      append(json.RawMessage{}, condition.Equals...),
	}
}

func capabilityCompletionEvidence(completionEvidence *config.CapabilityCompletionEvidence) *agentruntime.CapabilityCompletionEvidence {
	if completionEvidence == nil {
		return nil
	}
	return &agentruntime.CapabilityCompletionEvidence{
		Mode:       completionEvidence.Mode,
		Action:     completionEvidence.Action,
		TargetKind: completionEvidence.TargetKind,
	}
}

func deriveAllowedToolNamesByProfile(runtimeConfiguration config.RuntimeConfiguration) map[string][]string {
	allowedToolNamesByProfile := map[string][]string{}
	for _, agentProfile := range runtimeConfiguration.AgentProfiles {
		profileName := strings.TrimSpace(agentProfile.Name)
		if profileName == "" {
			profileName = "default"
		}
		allowedToolNamesByProfile[profileName] = appendDefaultBuiltInToolNames(agentProfile.AllowedToolNames)
	}
	return allowedToolNamesByProfile
}

func appendDefaultBuiltInToolNames(toolNames []string) []string {
	result := toolcontract.KernelToolNames()
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if toolcontract.IsKernelToolName(trimmedToolName) && !containsString(result, trimmedToolName) {
			result = append(result, trimmedToolName)
		}
	}
	return result
}

func containsString(values []string, expectedValue string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expectedValue {
			return true
		}
	}
	return false
}

func openRuntimeDatabase(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) (postgres.Database, error) {
	if strings.TrimSpace(runtimeConfiguration.Database.ConnectionString) == "" {
		return postgres.Database{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseInitializationTimeout)
	defer cancel()
	logger.Info("application.open_database.phase", "phase", "connect")
	database, errorValue := postgres.OpenDatabase(ctx, runtimeConfiguration.Database.ConnectionString)
	if errorValue != nil {
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "validate_migration_directory")
	migrationDirectoryPath := strings.TrimSpace(runtimeConfiguration.Database.MigrationDirectoryPath)
	if migrationDirectoryPath == "" {
		migrationDirectoryPath = "migrations"
	}
	migrationRunner := postgres.MigrationRunner{MigrationDirectoryPath: migrationDirectoryPath, Logger: logger}
	if errorValue := postgres.ValidateConnectorMigrationDirectory(migrationRunner); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "apply_migrations")
	if errorValue := migrationRunner.ApplyMigrations(ctx, database); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "validate_schema")
	if errorValue := postgres.ValidateConnectorDeliverySchema(ctx, database); errorValue != nil {
		_ = database.Close()
		return postgres.Database{}, errorValue
	}
	logger.Info("application.open_database.phase", "phase", "ready")
	return database, nil
}

func buildBackupManifest(runtimeConfiguration config.RuntimeConfiguration, database postgres.Database) backup.Manifest {
	databaseKind := "none"
	requiredArtifacts := []string{"policy", "workspace"}
	if database.SQL != nil {
		databaseKind = "postgres"
		requiredArtifacts = append(requiredArtifacts, "blueclaw-postgres-dump")
	}
	return backup.Manifest{
		ContractVersion: 1,
		BlueclawVersion: "main",
		SchemaVersion:   "012_graphiti_memory_metadata",
		PersistentDataRoots: []string{
			"/workspace/.blueclaw",
			graphitiKuzuPath(runtimeConfiguration),
			runtimeConfiguration.Terminal.WorkspaceRootPath,
		},
		DatabaseKind:            databaseKind,
		RequiredBackupArtifacts: requiredArtifacts,
	}
}

func graphitiKuzuPath(runtimeConfiguration config.RuntimeConfiguration) string {
	if strings.TrimSpace(runtimeConfiguration.Memory.GraphitiKuzuPath) != "" {
		return strings.TrimSpace(runtimeConfiguration.Memory.GraphitiKuzuPath)
	}
	return "/workspace/.blueclaw/graphiti/kuzu"
}

func newCapabilityClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint:       runtimeConfiguration.Capabilities.Endpoint,
		Transport:      runtimeConfiguration.Capabilities.Transport,
		UnixSocketPath: runtimeConfiguration.Capabilities.UnixSocketPath,
		VSockCID:       runtimeConfiguration.Capabilities.VSockCID,
		VSockPort:      runtimeConfiguration.Capabilities.VSockPort,
		Timeout:        time.Duration(runtimeConfiguration.Capabilities.TimeoutSecond) * time.Second,
	})
}

func newChatdClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint: firstNonEmptyString(runtimeConfiguration.Connectors.Chatd.Endpoint, connectors.DefaultChatdEndpoint),
		Timeout:  time.Duration(runtimeConfiguration.Connectors.Chatd.TimeoutSecond) * time.Second,
	})
}

func newPlatformAdapter(platform string, runtimeConfiguration config.RuntimeConfiguration, capabilityClient capability.Client, chatdClient capability.Client) connectors.PlatformAdapter {
	if isChatdEnabledForPlatform(runtimeConfiguration.Connectors.Chatd, platform) {
		return connectors.NewChatdPlatformAdapter(platform, chatdClient)
	}
	return connectors.NewCapabilityPlatformAdapter(platform, capabilityClient)
}

func isChatdEnabledForPlatform(chatdConfiguration config.ChatdConnectorConfiguration, platform string) bool {
	for _, enabledPlatform := range chatdConfiguration.EnabledPlatforms {
		if strings.EqualFold(strings.TrimSpace(enabledPlatform), platform) {
			return true
		}
	}
	return false
}

func skillIndexPath(runtimeConfiguration config.RuntimeConfiguration) string {
	workspaceRootPath := firstNonEmptyString(runtimeConfiguration.Terminal.WorkspaceRootPath, "/workspace")
	return filepath.Join(workspaceRootPath, ".blueclaw", "skill-index.json")
}

func resolveLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration) llm.LanguageModelProvider {
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	if strings.TrimSpace(languageModelConfiguration.LanguageModel.DefaultProvider) == "" {
		return nil
	}

	languageModelProvider, errorValue := llm.NewConfiguredLanguageModelProvider(
		languageModelConfiguration,
	)
	if errorValue != nil {
		return nil
	}

	return languageModelProvider
}

func resolveTaskTierLanguageModelProviders(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) agentcontract.TaskTierLanguageModels {
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	if strings.TrimSpace(languageModelConfiguration.LanguageModel.DefaultProvider) == "" {
		return agentcontract.TaskTierLanguageModels{}
	}
	tierNames := llm.ResolveModelTierNames(languageModelConfiguration)
	maximumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MaximumModelTier)
	minimumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MinimumModelTier)
	if maximumModelTier != "" {
		return resolveCappedTaskTierLanguageModelProviders(languageModelConfiguration, tierNames, minimumModelTier, maximumModelTier, logger)
	}
	if logger != nil {
		logger.Info("resolved task model tiers",
			"max", tierNames.Max,
			"xhigh", tierNames.XHigh,
			"high", tierNames.High,
			"medium", tierNames.Medium,
			"low", tierNames.Low,
			"xlow", tierNames.XLow)
	}
	hasConfigurationError := false
	configuredProvider := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(languageModelConfiguration, modelName)
		if errorValue != nil {
			hasConfigurationError = true
			if logger != nil {
				logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
			}
		}
		return provider
	}
	lowModel := llm.WithModelTier(configuredProvider(tierNames.Low), "low")
	xLowModel := llm.WithModelTier(configuredProvider(tierNames.XLow), "xlow")
	mediumModel := llm.WithModelTier(configuredProvider(tierNames.Medium), "medium")
	highModel := llm.WithModelTier(configuredProvider(tierNames.High), "high")
	xHighModel := llm.WithModelTier(configuredProvider(tierNames.XHigh), "xhigh")
	maxModel := llm.WithModelTier(configuredProvider(tierNames.Max), "max")
	if hasConfigurationError {
		return agentcontract.TaskTierLanguageModels{}
	}

	lowWithFallback := llm.LanguageModelProvider(lowModel)
	if tierNames.Medium != tierNames.Low {
		lowWithFallback = llm.FallbackLanguageModelProvider{
			PrimaryProvider:  lowModel,
			FallbackProvider: mediumModel,
			PrimaryLabel:     "low",
			FallbackLabel:    "medium",
			Logger:           logger,
		}
	}
	xLowWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  xLowModel,
		FallbackProvider: lowWithFallback,
		PrimaryLabel:     "xlow",
		FallbackLabel:    "low",
		Logger:           logger,
	}
	mediumWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  mediumModel,
		FallbackProvider: lowModel,
		PrimaryLabel:     "medium",
		FallbackLabel:    "low",
		Logger:           logger,
	}
	highWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  highModel,
		FallbackProvider: mediumWithFallback,
		PrimaryLabel:     "high",
		FallbackLabel:    "medium",
		Logger:           logger,
	}
	xHighWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  xHighModel,
		FallbackProvider: highWithFallback,
		PrimaryLabel:     "xhigh",
		FallbackLabel:    "high",
		Logger:           logger,
	}
	maxWithFallback := llm.FallbackLanguageModelProvider{
		PrimaryProvider:  maxModel,
		FallbackProvider: xHighWithFallback,
		PrimaryLabel:     "max",
		FallbackLabel:    "xhigh",
		Logger:           logger,
	}
	return agentcontract.TaskTierLanguageModels{
		Low:    lowWithFallback,
		XLow:   xLowWithFallback,
		Medium: mediumWithFallback,
		High:   highWithFallback,
		XHigh:  xHighWithFallback,
		Max:    maxWithFallback,
	}
}

func resolveIntakeLanguageModelProvider(runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) llm.LanguageModelProvider {
	if !runtimeConfiguration.Agent.Intake.Enabled {
		return nil
	}
	executionMode := strings.TrimSpace(runtimeConfiguration.Agent.Intake.ExecutionMode)
	if executionMode == "" {
		executionMode = "auto"
	}
	languageModelConfiguration := deriveLanguageModelRuntimeConfiguration(runtimeConfiguration)
	languageModelConfiguration.LanguageModel.Capability.ExecutionMode = executionMode
	tierNames := llm.ResolveModelTierNames(languageModelConfiguration)
	maximumModelTier := normalizeMaximumModelTier(languageModelConfiguration.LanguageModel.Capability.MaximumModelTier)
	hasConfigurationError := false
	configuredProvider := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(languageModelConfiguration, modelName)
		if errorValue == nil {
			return provider
		}
		hasConfigurationError = true
		if logger != nil {
			logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
		}
		return nil
	}
	if maximumModelTier != "" {
		providers := buildCappedModelTierProviders(tierNames, configuredProvider, logger)
		if hasConfigurationError {
			return nil
		}
		return providers.providerForTier(maximumModelTier)
	}
	primaryModelName := firstNonEmptyString(runtimeConfiguration.Agent.Intake.Model, tierNames.Medium)
	primaryProvider := configuredProvider(primaryModelName)
	if strings.TrimSpace(runtimeConfiguration.Agent.Intake.Model) == "" {
		primaryProvider = llm.WithModelTier(primaryProvider, "medium")
	}
	fallbackProvider := llm.WithModelTier(configuredProvider(tierNames.High), "high")
	if hasConfigurationError {
		return nil
	}
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider:  primaryProvider,
		FallbackProvider: fallbackProvider,
		PrimaryLabel:     "intake",
		FallbackLabel:    "high",
		Logger:           logger,
	}
}

type cappedModelTierProviders struct {
	xLow   llm.LanguageModelProvider
	low    llm.LanguageModelProvider
	medium llm.LanguageModelProvider
	high   llm.LanguageModelProvider
	xHigh  llm.LanguageModelProvider
	max    llm.LanguageModelProvider
}

func resolveCappedTaskTierLanguageModelProviders(runtimeConfiguration config.RuntimeConfiguration, tierNames llm.ModelTierNames, minimumModelTier string, maximumModelTier string, logger *slog.Logger) agentcontract.TaskTierLanguageModels {
	hasConfigurationError := false
	providerFactory := func(modelName string) llm.LanguageModelProvider {
		provider, errorValue := llm.NewConfiguredLanguageModelProviderForModel(runtimeConfiguration, modelName)
		if errorValue == nil {
			return provider
		}
		hasConfigurationError = true
		if logger != nil {
			logger.Error("language model provider configuration failed", "model", modelName, "error", errorValue.Error())
		}
		return nil
	}
	providers := buildCappedModelTierProviders(tierNames, providerFactory, logger)
	if hasConfigurationError {
		return agentcontract.TaskTierLanguageModels{}
	}
	if logger != nil {
		logger.Info("resolved capped task model tiers", "maximumModelTier", maximumModelTier, "xlow", tierNames.XLow, "lowVision", tierNames.Low)
	}
	return agentcontract.TaskTierLanguageModels{
		Low:    providers.providerWithinBounds("low", minimumModelTier, maximumModelTier),
		XLow:   providers.providerWithinBounds("xlow", minimumModelTier, maximumModelTier),
		Medium: providers.providerWithinBounds("medium", minimumModelTier, maximumModelTier),
		High:   providers.providerWithinBounds("high", minimumModelTier, maximumModelTier),
		XHigh:  providers.providerWithinBounds("xhigh", minimumModelTier, maximumModelTier),
		Max:    providers.providerWithinBounds("max", minimumModelTier, maximumModelTier),
	}
}

func buildCappedModelTierProviders(tierNames llm.ModelTierNames, providerFactory func(string) llm.LanguageModelProvider, logger *slog.Logger) cappedModelTierProviders {
	xLowModel := llm.WithModelTier(providerFactory(tierNames.XLow), "xlow")
	lowModel := llm.WithModelTier(providerFactory(tierNames.Low), "low")
	lowProvider := descendingFallbackProvider(lowModel, xLowModel, "low", "xlow", logger)
	xLowProvider := llm.VisionFallbackProvider{TextOnlyModel: xLowModel, VisionModel: lowProvider}
	mediumProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.Medium), "medium"), lowProvider, "medium", "low", logger)
	highProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.High), "high"), mediumProvider, "high", "medium", logger)
	xHighProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.XHigh), "xhigh"), highProvider, "xhigh", "high", logger)
	maxProvider := descendingFallbackProvider(llm.WithModelTier(providerFactory(tierNames.Max), "max"), xHighProvider, "max", "xhigh", logger)
	return cappedModelTierProviders{xLow: xLowProvider, low: lowProvider, medium: mediumProvider, high: highProvider, xHigh: xHighProvider, max: maxProvider}
}

func descendingFallbackProvider(primaryProvider llm.LanguageModelProvider, fallbackProvider llm.LanguageModelProvider, primaryLabel string, fallbackLabel string, logger *slog.Logger) llm.LanguageModelProvider {
	return llm.FallbackLanguageModelProvider{
		PrimaryProvider: primaryProvider, FallbackProvider: fallbackProvider,
		PrimaryLabel: primaryLabel, FallbackLabel: fallbackLabel, Logger: logger,
	}
}

func (providers cappedModelTierProviders) providerWithinBounds(requestedTier string, minimumModelTier string, maximumModelTier string) llm.LanguageModelProvider {
	boundedTier := requestedTier
	if minimumModelTier != "" && modelTierRank(boundedTier) < modelTierRank(minimumModelTier) {
		boundedTier = minimumModelTier
	}
	if modelTierRank(boundedTier) > modelTierRank(maximumModelTier) {
		boundedTier = maximumModelTier
	}
	return providers.providerForTier(boundedTier)
}

func (providers cappedModelTierProviders) providerForTier(modelTier string) llm.LanguageModelProvider {
	switch normalizeMaximumModelTier(modelTier) {
	case "max":
		return providers.max
	case "xhigh":
		return providers.xHigh
	case "high":
		return providers.high
	case "medium":
		return providers.medium
	case "low":
		return providers.low
	default:
		return providers.xLow
	}
}

func normalizeMaximumModelTier(modelTier string) string {
	normalizedModelTier := strings.ToLower(strings.TrimSpace(modelTier))
	for _, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if normalizedModelTier == supportedModelTier {
			return supportedModelTier
		}
	}
	return ""
}

func modelTierRank(modelTier string) int {
	for rank, supportedModelTier := range []string{"xlow", "low", "medium", "high", "xhigh", "max"} {
		if normalizeMaximumModelTier(modelTier) == supportedModelTier {
			return rank
		}
	}
	return 0
}

func deriveLanguageModelRuntimeConfiguration(runtimeConfiguration config.RuntimeConfiguration) config.RuntimeConfiguration {
	if strings.TrimSpace(runtimeConfiguration.LanguageModel.DefaultProvider) == "" {
		runtimeConfiguration.LanguageModel.DefaultProvider = "capabilityLLM"
	}
	runtimeConfiguration.LanguageModel.FallbackProvider = ""
	return runtimeConfiguration
}

func (application *Application) Start() error {
	if application.startupError != nil {
		return application.startupError
	}
	if errorValue := application.checkProtocolIdentity(); errorValue != nil {
		application.runtimeLogger.Logger.Error("application.protocol_identity_rejected", "error", errorValue.Error())
		return application.serveWithoutStartingWork()
	}
	if application.refreshSkillIndex != nil {
		go application.refreshSkillIndex(context.Background())
	}
	application.runtimeLogger.Logger.Info("application.starting", "stage", "log_retention")
	application.startLogRetentionLoop()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "memory_queue")
	application.startMemoryUpdateQueue()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "connector_runtime")
	application.startConnectorRuntime()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "connector_transports")
	application.startConnectorTransports()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "task_schedule")
	application.startTaskSchedulePoller()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "task_retention")
	application.startTaskRetentionSweeper()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "stale_tasks")
	application.startStaleTaskSweeper()
	application.runtimeLogger.Logger.Info("application.starting", "stage", "listen")
	listener, errorValue := net.Listen("tcp", application.httpServer.Addr)
	if errorValue != nil {
		return errorValue
	}
	application.runtimeLogger.Logger.Info(
		"application.started",
		"listenAddress",
		application.httpServer.Addr,
		"connectorTransports",
		strings.Join(application.connectorTransportNames(), ","),
		"languageModelDefaultProvider",
		application.languageModelDefaultProvider,
		"languageModelFallbackProvider",
		application.languageModelFallbackProvider,
		"languageModelConfigured",
		application.languageModelConfigured,
		"logDirectoryPath",
		application.runtimeLogger.DirectoryPath(),
	)
	application.startInterruptedTaskAutoResume()
	return application.httpServer.Serve(listener)
}

// A contract this build does not share would have the agent call tool names
// the other side never registered, so no work may run. Exiting instead leaves
// the supervisor restarting every few seconds, which also takes down the one
// endpoint where the disagreement is visible to the host.
func (application *Application) serveWithoutStartingWork() error {
	listener, errorValue := net.Listen("tcp", application.httpServer.Addr)
	if errorValue != nil {
		return errorValue
	}
	application.runtimeLogger.Logger.Info(
		"application.serving_health_only",
		"listenAddress",
		application.httpServer.Addr,
	)
	return application.httpServer.Serve(listener)
}

// expectedProtocolIdentity prefers what the appliance pinned, and otherwise
// falls back to the contract this build was generated from.
func expectedProtocolIdentity(runtimeConfiguration config.RuntimeConfiguration) protocolidentity.Identity {
	if strings.TrimSpace(runtimeConfiguration.Capabilities.ProtocolVersion) != "" {
		return protocolidentity.Identity{
			ProtocolVersion:       runtimeConfiguration.Capabilities.ProtocolVersion,
			AggregateProtocolHash: runtimeConfiguration.Capabilities.AggregateProtocolHash,
		}
	}
	builtIdentity := capabilitycatalog.BuiltProtocolIdentity()
	return protocolidentity.Identity{
		ProtocolVersion:       builtIdentity.ProtocolVersion,
		AggregateProtocolHash: builtIdentity.AggregateProtocolHash,
	}
}

func (application *Application) checkProtocolIdentity() error {
	application.protocolIdentityCheckOnce.Do(func() {
		result := application.protocolIdentityChecker.Check(context.Background(), application.protocolIdentityExpected)
		*application.protocolIdentityStatus = result
		if !result.Passed {
			application.protocolIdentityCheckError = fmt.Errorf("protocol identity check failed: %s", strings.Join(result.FailureReasons, "; "))
		}
	})
	return application.protocolIdentityCheckError
}

// Handler exposes the built HTTP surface so a caller can exercise the runtime
// without binding a port.
func (application *Application) Handler() http.Handler {
	if application.httpServer == nil {
		return nil
	}
	return application.httpServer.Handler
}

func (application *Application) Shutdown(ctx context.Context) error {
	if application.connectorTransportCancel != nil {
		application.connectorTransportCancel()
	}
	if application.connectorRuntimeCancel != nil {
		application.connectorRuntimeCancel()
	}
	if application.taskScheduleCancel != nil {
		application.taskScheduleCancel()
	}
	if application.taskRetentionCancel != nil {
		application.taskRetentionCancel()
	}
	if application.staleTaskCancel != nil {
		application.staleTaskCancel()
	}
	if application.interruptedTaskResumeCancel != nil {
		application.interruptedTaskResumeCancel()
	}
	if application.logRetentionCancel != nil {
		application.logRetentionCancel()
	}
	if application.memoryUpdateCancel != nil {
		application.memoryUpdateCancel()
	}
	errorValue := application.httpServer.Shutdown(ctx)
	backgroundError := application.awaitBackgroundLoops(ctx)
	terminalCloseError := application.closeTerminalSessions()
	mcpCloseError := application.closeMCPRegistry()
	closeErrorValue := application.runtimeLogger.Close()
	databaseCloseError := application.database.Close()
	if errorValue != nil {
		return errorValue
	}
	if backgroundError != nil {
		return backgroundError
	}
	if terminalCloseError != nil {
		return terminalCloseError
	}
	if mcpCloseError != nil {
		return mcpCloseError
	}
	if closeErrorValue != nil {
		return closeErrorValue
	}
	return databaseCloseError
}

// Cancelling a context asks a goroutine to stop. Nothing was waiting for one to have
// stopped, so Shutdown closed the database while sweepers were still writing to it.
func (application *Application) startBackgroundLoop(run func(context.Context)) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	application.backgroundLoops.Add(1)
	go func() {
		defer application.backgroundLoops.Done()
		run(ctx)
	}()
	return cancel
}

func (application *Application) awaitBackgroundLoops(ctx context.Context) error {
	stopped := make(chan struct{})
	go func() {
		application.backgroundLoops.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return errors.New("shutdown ran out of time before its background loops stopped")
	case <-time.After(backgroundLoopStopGrace):
		return errors.New("background loops did not stop within " + backgroundLoopStopGrace.String())
	}
}

func (application *Application) closeTerminalSessions() error {
	if application.terminalService == nil {
		return nil
	}
	return application.terminalService.CloseAllSessions()
}

func (application *Application) closeMCPRegistry() error {
	if application.mcpRegistry == nil {
		return nil
	}
	return application.mcpRegistry.Close()
}

func (application *Application) startConnectorRuntime() {
	if application.connectorRuntime == nil || application.connectorRuntimeCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.connectorRuntimeCancel = cancel
	application.connectorRuntime.Start(ctx)
}

func (application *Application) startConnectorTransports() {
	if len(application.connectorTransports) == 0 || application.connectorTransportCancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	application.connectorTransportCancel = cancel
	for _, connectorTransport := range application.connectorTransports {
		transport := connectorTransport
		application.runtimeLogger.Logger.Info(
			"connector."+transport.Platform()+".transport.registered",
			"name",
			transport.Name(),
			"platform",
			transport.Platform(),
		)
		application.backgroundLoops.Add(1)
		go func() {
			defer application.backgroundLoops.Done()
			transport.Start(ctx)
		}()
	}
}

func (application *Application) connectorTransportNames() []string {
	transportNames := make([]string, 0, len(application.connectorTransports))
	for _, connectorTransport := range application.connectorTransports {
		transportNames = append(transportNames, connectorTransport.Platform()+":"+connectorTransport.Name())
	}
	return transportNames
}

func (application *Application) startLogRetentionLoop() {
	if application.runtimeLogger == nil || application.logRetentionCancel != nil {
		return
	}

	application.logRetentionCancel = application.startBackgroundLoop(application.runtimeLogger.StartRetentionLoop)
}

func (application *Application) startTaskSchedulePoller() {
	if application.taskSchedulePoller == nil || application.taskScheduleCancel != nil {
		return
	}
	interval := time.Duration(application.taskSchedulePollIntervalSecond()) * time.Second
	application.taskScheduleCancel = application.startBackgroundLoop(func(ctx context.Context) {
		application.taskSchedulePoller.Start(ctx, interval)
	})
}

func (application *Application) startTaskRetentionSweeper() {
	if application.taskRetentionSweeper == nil || application.taskRetentionCancel != nil {
		return
	}
	interval := time.Duration(application.taskRetentionIntervalMinuteOrDefault()) * time.Minute
	application.taskRetentionCancel = application.startBackgroundLoop(func(ctx context.Context) {
		application.taskRetentionSweeper.Start(ctx, interval)
	})
}

func (application *Application) startStaleTaskSweeper() {
	if application.taskRunService == nil || application.staleTaskCancel != nil {
		return
	}
	sweeper := scheduler.StaleTaskSweeper{
		TaskRunService: application.taskRunService,
		Notifier:       application.interruptedTaskResumer,
		Logger:         application.runtimeLogger.Logger,
	}
	application.staleTaskCancel = application.startBackgroundLoop(func(ctx context.Context) {
		sweeper.Start(ctx, 30*time.Minute)
	})
}

func (application *Application) startInterruptedTaskAutoResume() {
	if application.taskRunService == nil || application.interruptedTaskResumer == nil || application.interruptedTaskResumeCancel != nil {
		return
	}
	resumeStartedAt := time.Now()
	application.interruptedTaskResumeCancel = application.startBackgroundLoop(func(ctx context.Context) {
		application.resumeInterruptedTaskRuns(ctx, resumeStartedAt)
	})
}

func (application *Application) resumeInterruptedTaskRuns(ctx context.Context, now time.Time) {
	selection := application.taskRunService.SelectInterruptedTaskRunsForAutoResume(now, 5)
	for _, taskRun := range selection.SkippedTaskRuns {
		application.taskRunService.MarkInterruptedTaskRunAutoResumeSkipped(taskRun.TaskRunID, "per_boot_limit_exceeded")
	}
	for index, taskRun := range selection.SelectedTaskRuns {
		if ctx.Err() != nil {
			return
		}
		if index > 0 && !application.waitBeforeInterruptedTaskResume(ctx) {
			return
		}
		if !application.interruptedTaskResumer.CanResumeInterruptedTaskRun(taskRun) {
			application.taskRunService.MarkInterruptedTaskRunAutoResumeSkipped(taskRun.TaskRunID, "resume_context_unavailable")
			continue
		}
		if !application.taskRunService.ClaimInterruptedTaskRunAutoResume(taskRun.TaskRunID, "runtime_restart") {
			continue
		}
		if _, errorValue := application.interruptedTaskResumer.ResumeInterruptedTaskRun(ctx, taskRun); errorValue != nil {
			application.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "task.auto_resume_launch_failed", errorValue.Error())
		}
	}
	application.failUnresumedInterruptedTaskRuns(ctx)
}

func (application *Application) failUnresumedInterruptedTaskRuns(ctx context.Context) {
	for _, taskRun := range application.taskRunService.ListTaskRun() {
		if ctx.Err() != nil {
			return
		}
		if !task.TaskRunWasInterruptedByRuntimeRestart(taskRun) {
			continue
		}
		application.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "task.auto_resume_abandoned", taskRun.FailureReason)
		application.interruptedTaskResumer.FailUnresumedInterruptedTaskRun(ctx, taskRun, "the task was interrupted by a runtime restart and could not be resumed")
	}
}

func (application *Application) waitBeforeInterruptedTaskResume(ctx context.Context) bool {
	delay := application.interruptedTaskResumeDelay
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (application *Application) taskRetentionIntervalMinuteOrDefault() int {
	if application.taskRetentionIntervalMinute > 0 {
		return application.taskRetentionIntervalMinute
	}
	return 60
}

func (application *Application) startMemoryUpdateQueue() {
	if application.memoryUpdateQueue == nil || application.memoryUpdateCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	application.memoryUpdateCancel = cancel
	application.memoryUpdateQueue.Start(ctx)
}

func (application *Application) taskSchedulePollIntervalSecond() int {
	if application.taskSchedulePollSecond > 0 {
		return application.taskSchedulePollSecond
	}
	return 30
}

func deriveListenAddress(baseURL string) string {
	if baseURL == "" {
		return "127.0.0.1:8080"
	}

	parsedURL, errorValue := url.Parse(baseURL)
	if errorValue != nil || parsedURL.Host == "" {
		return baseURL
	}

	return parsedURL.Host
}

func classificationLanguageModelProvider(taskTierLanguageModels agentcontract.TaskTierLanguageModels, intakeLanguageModelProvider model.LanguageModelProvider) model.LanguageModelProvider {
	if taskTierLanguageModels.XLow != nil {
		return taskTierLanguageModels.XLow
	}
	if intakeLanguageModelProvider != nil {
		return intakeLanguageModelProvider
	}
	return taskTierLanguageModels.High
}

func turnRouterLanguageModelProvider(taskTierLanguageModels agentcontract.TaskTierLanguageModels, intakeLanguageModelProvider model.LanguageModelProvider) model.LanguageModelProvider {
	if intakeLanguageModelProvider != nil {
		return intakeLanguageModelProvider
	}
	return taskTierLanguageModels.High
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

func newToolCatalogSessionToken() string {
	sessionToken := make([]byte, 32)
	if _, errorValue := rand.Read(sessionToken); errorValue != nil {
		return ""
	}
	return hex.EncodeToString(sessionToken)
}

func toolCatalogURL(runtimeConfiguration config.RuntimeConfiguration) string {
	if configuredURL := strings.TrimSpace(runtimeConfiguration.Agent.Harness.ToolCatalogURL); configuredURL != "" {
		return configuredURL
	}
	return "http://" + deriveListenAddress(runtimeConfiguration.BaseURL) + "/harness/tool-catalog"
}

func currentExecutablePath() string {
	executablePath, errorValue := os.Executable()
	if errorValue != nil {
		return ""
	}
	return executablePath
}
