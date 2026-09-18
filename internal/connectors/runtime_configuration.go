package connectors

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (connectorRuntime *ConnectorRuntime) UseUnknownAccountResolver(unknownAccountResolver UnknownAccountResolver) {
	connectorRuntime.unknownAccountResolver = unknownAccountResolver
}

func (connectorRuntime *ConnectorRuntime) UseAdminTaskLinkBaseURL(adminTaskLinkBaseURL string) {
	connectorRuntime.adminTaskLinkBaseURL = strings.TrimRight(strings.TrimSpace(adminTaskLinkBaseURL), "/")
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceID(workspaceID string) {
	connectorRuntime.workspaceID = strings.TrimSpace(workspaceID)
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceRootPath(workspaceRootPath string) {
	connectorRuntime.toolCatalogBuilder.UseWorkspaceRootPath(workspaceRootPath)
}

func (connectorRuntime *ConnectorRuntime) UseTerminalService(terminalService *security.ShellService) {
	connectorRuntime.toolCatalogBuilder.UseTerminalService(terminalService)
}

func (connectorRuntime *ConnectorRuntime) UseWorkspaceActorFactory(workspaceActorFactory security.WorkspaceActorFactory) {
	connectorRuntime.workspaceActorFactory = workspaceActorFactory
	connectorRuntime.toolCatalogBuilder.UseWorkspaceActorFactory(workspaceActorFactory)
}

func (connectorRuntime *ConnectorRuntime) UseTaskWaitTokenRepository(taskWaitTokenRepository task.TaskWaitTokenRepository) {
	connectorRuntime.taskWaitTokenRepository = taskWaitTokenRepository
}

func (connectorRuntime *ConnectorRuntime) UseApprovalGate(approvalGate *approvalgate.Gate) {
	connectorRuntime.approvalGate = approvalGate
}

func (connectorRuntime *ConnectorRuntime) UseTaskRunService(taskRunService *task.TaskRunService) {
	connectorRuntime.toolCatalogBuilder.UseTaskRunService(taskRunService)
}

func (connectorRuntime *ConnectorRuntime) UseEventRepository(eventRepository ConnectorEventRepository) {
	connectorRuntime.eventRepository = eventRepository
}

func (connectorRuntime *ConnectorRuntime) UseIngressGate(ingressGate IngressGate) {
	connectorRuntime.ingressGate = ingressGate
}

func (connectorRuntime *ConnectorRuntime) UseTaskIntakeGate(taskIntakeGate TaskIntakeGate) {
	connectorRuntime.taskIntakeGate = taskIntakeGate
}

func (connectorRuntime *ConnectorRuntime) UseCapabilityToolDescriptors(capabilityClient capability.Client, toolDescriptors []agentruntime.CapabilityToolDescriptor) {
	connectorRuntime.toolCatalogBuilder.UseCapabilityToolDescriptors(capabilityClient, toolDescriptors)
}

func (connectorRuntime *ConnectorRuntime) UseAllowedToolNames(allowedToolNames []string) {
	trimmedToolNames := trimNonEmptyStrings(allowedToolNames)
	if len(trimmedToolNames) == 0 {
		trimmedToolNames = connectorRuntimeDefaultAllowedToolNames()
	}
	connectorRuntime.toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, trimmedToolNames)
}

func connectorRuntimeDefaultAllowedToolNames() []string {
	return append([]string{"conversation_history"}, agentruntime.DefaultAllowedToolNames()...)
}

func (connectorRuntime *ConnectorRuntime) UseAllowedToolNamesByProfile(allowedToolNamesByProfile map[string][]string, defaultAllowedToolNames []string) {
	connectorRuntime.toolCatalogBuilder.UseAllowedToolNamesByProfile(allowedToolNamesByProfile, defaultAllowedToolNames)
}

type LaunchFailureCompleter interface {
	CompleteLaunchFailure(context.Context, agentcontract.AgentTurnRequest, string, string, error) agentcontract.AgentTurnResult
}

func (connectorRuntime *ConnectorRuntime) UseLaunchFailureCompleter(launchFailureCompleter LaunchFailureCompleter) {
	connectorRuntime.launchFailureCompleter = launchFailureCompleter
}

type ReplyGenerator interface {
	GenerateReply(context.Context, string) (string, error)
	GenerateReplyWithContext(context.Context, string, agentcontract.VisibleContext, []agentcontract.MemoryFact) (string, error)
}

func (connectorRuntime *ConnectorRuntime) UseReplyGenerator(replyGenerator ReplyGenerator) {
	connectorRuntime.replyGenerator = replyGenerator
}

type TurnRouter interface {
	Plan(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error)
	PlanObserved(context.Context, agentcontract.AgentRequest, *agentcontract.IntakeCallLedger) (agentcontract.TurnDecision, error)
}

func (connectorRuntime *ConnectorRuntime) UseTurnRouter(turnRouter TurnRouter) {
	connectorRuntime.turnRouter = turnRouter
}

func (connectorRuntime *ConnectorRuntime) UseTaskLauncher(taskLauncher *agentruntime.TaskLauncher) {
	connectorRuntime.taskLauncher = taskLauncher
}

func (connectorRuntime *ConnectorRuntime) UseCompanyProvider(companyProvider func() agentcontract.CompanyContext) {
	connectorRuntime.companyProvider = companyProvider
}

func (connectorRuntime *ConnectorRuntime) company() agentcontract.CompanyContext {
	if connectorRuntime.companyProvider == nil {
		return agentcontract.CompanyContext{}
	}
	return connectorRuntime.companyProvider()
}

func (connectorRuntime *ConnectorRuntime) UseCompanyLocaleProvider(companyLocaleProvider func() string) {
	connectorRuntime.companyLocaleProvider = companyLocaleProvider
}

func (connectorRuntime *ConnectorRuntime) companyLocale() string {
	if connectorRuntime.companyLocaleProvider == nil {
		return normalizeCompanyReplyLocale("")
	}
	return normalizeCompanyReplyLocale(connectorRuntime.companyLocaleProvider())
}

func (connectorRuntime *ConnectorRuntime) UseAgentIdentityProvider(agentIdentityProvider func() agentcontract.AgentIdentity) {
	connectorRuntime.agentIdentityProvider = agentIdentityProvider
}

func (connectorRuntime *ConnectorRuntime) agentIdentity() agentcontract.AgentIdentity {
	if connectorRuntime.agentIdentityProvider == nil {
		return agentcontract.AgentIdentity{}
	}
	return connectorRuntime.agentIdentityProvider()
}
