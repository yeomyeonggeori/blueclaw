package app

import (
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/backup"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	apiconnector "github.com/yeomyeonggeori/blueclaw/internal/connectors/api"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/reply"
	"github.com/yeomyeonggeori/blueclaw/internal/runtimecontrol"
	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
	capabilitycatalog "github.com/yeomyeonggeori/blueclaw/protocol/generated"
	"github.com/yeomyeonggeori/bluecollar/intake"
)

func newConnectorRuntime(runtimeConfiguration config.RuntimeConfiguration, foundation runtimeFoundation, directory identityDirectory, kernel agentKernel, services taskServices, memoryComponents memoryComponents, taskLauncher *agentruntime.TaskLauncher, turnRouter intake.TurnRouter, backupCoordinator *backup.Coordinator, taskIntakeController *runtimecontrol.TaskIntakeController) *connectors.ConnectorRuntime {
	logger := foundation.logger
	languageModelProvider := kernel.taskTierLanguageModels.High
	connectorRuntime := connectors.NewConnectorRuntime(
		directory.identityService,
		kernel.harness,
		services.taskRunService,
		services.taskEventService,
		logger,
	)
	launchFailureCompleter := launchfailure.NewCompleter(services.taskRunService, languageModelProvider)
	connectorRuntime.UseUnknownAccountResolver(connectors.NewCapabilityUnknownAccountResolver(kernel.capabilityClient))
	connectorRuntime.UseLaunchFailureCompleter(launchFailureCompleter)
	replyGenerator := reply.NewGenerator(languageModelProvider, kernel.instructionBundleLoader)
	replyGenerator.UseAgentIdentityProvider(kernel.agentIdentityProvider)
	replyGenerator.UseCompanyProvider(directory.companyProvider)
	connectorRuntime.UseReplyGenerator(replyGenerator)
	connectorRuntime.UseCompanyProvider(directory.companyProvider)
	connectorRuntime.UseTurnRouter(turnRouter)
	connectorRuntime.UseIntakeClassifier(intake.NewClassifier(classificationLanguageModelProvider(kernel.taskTierLanguageModels, kernel.intakeLanguageModelProvider)))
	connectorRuntime.UseTaskLauncher(taskLauncher)
	connectorRuntime.UseApprovalGate(kernel.toolCatalog.approvalGate)
	connectorRuntime.UseAgentIdentityProvider(kernel.agentIdentityProvider)
	connectorRuntime.UseAllowedToolNamesByProfile(deriveAllowedToolNamesByProfile(runtimeConfiguration), deriveAllowedToolNames(runtimeConfiguration))
	connectorRuntime.UseMemoryService(memoryComponents.memoryService)
	connectorRuntime.UseWorkspaceID(runtimeConfiguration.Memory.WorkspaceID)
	connectorRuntime.UseAdminTaskLinkBaseURL(runtimeConfiguration.Agent.AdminTaskLinkBaseURL)
	connectorRuntime.UseWorkspaceActorFactory(kernel.terminalService.WorkspaceActorFactory())
	connectorRuntime.UseIngressGate(backupCoordinator)
	connectorRuntime.UseTaskIntakeGate(taskIntakeController)
	if foundation.database.SQL != nil {
		connectorRuntime.UseEventRepository(postgres.NewRawEventRepository(foundation.database))
	}
	return connectorRuntime
}

func registerChatdAdapters(connectorRuntime *connectors.ConnectorRuntime, runtimeConfiguration config.RuntimeConfiguration, logger *slog.Logger) {
	chatdClient := newChatdClient(runtimeConfiguration)
	for _, platform := range capabilitycatalog.MessengerPlatformNames() {
		connectorRuntime.RegisterAdapter(connectors.NewChatdPlatformAdapter(platform, chatdClient))
	}
	for _, platform := range platformsChatdServesBeyondTheProtocol(runtimeConfiguration.Connectors.Chatd) {
		logger.Warn("connector.platform.served_beyond_the_protocol", "platform", platform)
		connectorRuntime.RegisterAdapter(connectors.NewChatdPlatformAdapter(platform, chatdClient))
	}
}

func newAgentReplyStore(runtimeConfiguration config.RuntimeConfiguration) *apiconnector.ReplyStore {
	return apiconnector.NewPersistentReplyStore(filepath.Join(runtimeConfiguration.Terminal.WorkspaceRootPath, ".blueclaw", "state", "agent-replies.json"))
}

func newConnectorTransports() []connectors.ConnectorTransport {
	connectorTransports := []connectors.ConnectorTransport{}
	for _, platform := range capabilitycatalog.MessengerPlatformNames() {
		connectorTransports = append(connectorTransports, connectors.NewHTTPWebhookTransport(platform+"-internal-ingress", platform))
	}
	return connectorTransports
}

func newChatdClient(runtimeConfiguration config.RuntimeConfiguration) capability.Client {
	return capability.NewClient(capability.Configuration{
		Endpoint: firstNonEmptyString(runtimeConfiguration.Connectors.Chatd.Endpoint, connectors.DefaultChatdEndpoint),
		Timeout:  time.Duration(runtimeConfiguration.Connectors.Chatd.TimeoutSecond) * time.Second,
	})
}

func platformsChatdServesBeyondTheProtocol(chatdConfiguration config.ChatdConnectorConfiguration) []string {
	platforms := []string{}
	for _, enabledPlatform := range chatdConfiguration.EnabledPlatforms {
		platform := strings.ToLower(strings.TrimSpace(enabledPlatform))
		if platform == "" || capabilitycatalog.IsConnectorPlatform(platform) || slices.Contains(platforms, platform) {
			continue
		}
		platforms = append(platforms, platform)
	}
	return platforms
}
