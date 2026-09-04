package app

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/backup"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	apiconnector "github.com/yeomyeonggeori/blueclaw/internal/connectors/api"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/httpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/protocolidentity"
	runtimelogging "github.com/yeomyeonggeori/blueclaw/internal/runtime"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/scheduler"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
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

type applicationComponents struct {
	runtimeConfiguration  config.RuntimeConfiguration
	policyPath            string
	foundation            runtimeFoundation
	directory             identityDirectory
	services              taskServices
	kernel                agentKernel
	memory                memoryComponents
	backupCoordinator     *backup.Coordinator
	taskIntakeController  *runtimecontrol.TaskIntakeController
	mcpRegistry           *mcp.McpRegistry
	toolCatalogBuilder    *agentruntime.ToolCatalogBuilder
	turnRouter            intake.TurnRouter
	taskLauncher          *agentruntime.TaskLauncher
	taskSchedulePoller    *scheduler.TaskSchedulePoller
	taskRetentionSweeper  *scheduler.TaskRetentionSweeper
	connectorRuntime      *connectors.ConnectorRuntime
	agentReplyStore       *apiconnector.ReplyStore
	connectorEventHandler *httpserver.ConnectorEventHandler
	protocolIdentity      protocolIdentityComponents
	router                http.Handler
	startupError          error
}

func NewApplication(runtimeConfiguration config.RuntimeConfiguration, policyPath string, agentHarnessFactory harnessdriver.Factory) *Application {
	return newApplication(newApplicationComponents(runtimeConfiguration, policyPath, agentHarnessFactory))
}

func newApplicationComponents(runtimeConfiguration config.RuntimeConfiguration, policyPath string, agentHarnessFactory harnessdriver.Factory) applicationComponents {
	components := applicationComponents{runtimeConfiguration: runtimeConfiguration, policyPath: policyPath}
	components.foundation = newRuntimeFoundation(runtimeConfiguration, policyPath)
	logger := components.foundation.logger
	components.directory = newIdentityDirectory(components.foundation.database, components.foundation.policyDocument, logger)
	components.services = newTaskServices(runtimeConfiguration, components.foundation.database, components.directory.companyProvider, logger)
	components.kernel = newAgentKernel(runtimeConfiguration, agentHarnessFactory, components.services, components.directory.companyProvider, logger)
	components.memory = newMemoryComponents(runtimeConfiguration, components.foundation.database, components.kernel.taskTierLanguageModels.Low, logger)
	components.backupCoordinator = backup.NewCoordinator(buildBackupManifest(runtimeConfiguration, components.foundation.database))
	components.taskIntakeController = runtimecontrol.NewTaskIntakeController()
	components.mcpRegistry = mcp.NewMcpRegistry()
	logMCPServerQuarantines(logger, components.mcpRegistry.LoadServerDefinition(runtimeConfiguration.MCPServers))
	components.toolCatalogBuilder = newToolCatalogBuilder(runtimeConfiguration, components.kernel, components.services, components.memory, components.mcpRegistry, logger)
	components.turnRouter = intake.NewTurnRouter(turnRouterLanguageModelProvider(components.kernel.taskTierLanguageModels, components.kernel.intakeLanguageModelProvider), deriveIntakeOptions(runtimeConfiguration))
	components.taskLauncher = newTaskLauncher(runtimeConfiguration, components.foundation, components.directory, components.kernel, components.services, components.toolCatalogBuilder, components.turnRouter)
	components.taskSchedulePoller = newTaskSchedulePoller(runtimeConfiguration, components.services, components.directory.identityService, components.taskLauncher, components.taskIntakeController, logger)
	logger.Info("application.initializing", "stage", "connector_runtime")
	components.taskRetentionSweeper = newTaskRetentionSweeper(runtimeConfiguration, components.services, logger)
	components.connectorRuntime = newConnectorRuntime(runtimeConfiguration, components.foundation, components.directory, components.kernel, components.services, components.memory, components.taskLauncher, components.turnRouter, components.backupCoordinator, components.taskIntakeController)
	registerChatdAdapters(components.connectorRuntime, runtimeConfiguration, logger)
	components.agentReplyStore = newAgentReplyStore(runtimeConfiguration)
	components.connectorRuntime.RegisterAdapter(apiconnector.NewAdapter(components.directory.identityService, components.agentReplyStore))
	components.connectorEventHandler = httpserver.NewConnectorEventHandler(components.connectorRuntime)
	logger.Info("application.initializing", "stage", "router")
	components.protocolIdentity = newProtocolIdentity(runtimeConfiguration, components.kernel.capabilityClient)
	components.startupError = firstNonNilError(components.foundation.startupError, components.kernel.startupError)
	components.router = httpserver.NewRouter(newRouterDependencies(components))
	return components
}

func newApplication(components applicationComponents) *Application {
	connectorTransports := newConnectorTransports()
	components.foundation.logger.Info("application.initializing", "stage", "ready")
	return &Application{
		httpServer: &http.Server{
			Addr:    deriveListenAddress(components.runtimeConfiguration.BaseURL),
			Handler: components.router,
		},
		connectorRuntime:              components.connectorRuntime,
		connectorTransports:           connectorTransports,
		taskRunService:                components.services.taskRunService,
		interruptedTaskResumer:        components.connectorRuntime,
		runtimeLogger:                 components.foundation.runtimeLogger,
		terminalService:               components.kernel.terminalService,
		database:                      components.foundation.database,
		startupError:                  components.startupError,
		taskSchedulePoller:            components.taskSchedulePoller,
		taskRetentionSweeper:          components.taskRetentionSweeper,
		memoryUpdateQueue:             components.memory.memoryUpdateQueue,
		taskSchedulePollSecond:        components.runtimeConfiguration.Scheduler.TaskSchedulePollIntervalSecond,
		taskRetentionIntervalMinute:   components.runtimeConfiguration.Scheduler.RetentionCheckIntervalMinute,
		interruptedTaskResumeDelay:    2 * time.Second,
		languageModelDefaultProvider:  components.kernel.languageModelRuntimeConfiguration.LanguageModel.DefaultProvider,
		languageModelFallbackProvider: components.kernel.languageModelRuntimeConfiguration.LanguageModel.FallbackProvider,
		languageModelConfigured:       components.kernel.taskTierLanguageModels.High != nil,
		protocolIdentityChecker:       components.protocolIdentity.checker,
		protocolIdentityExpected:      components.protocolIdentity.expected,
		protocolIdentityStatus:        components.protocolIdentity.status,
		refreshSkillIndex:             components.kernel.refreshSkillIndex,
		mcpRegistry:                   components.mcpRegistry,
	}
}

func firstNonNilError(errorValues ...error) error {
	for _, errorValue := range errorValues {
		if errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
