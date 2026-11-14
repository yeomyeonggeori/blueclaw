package harnessselection

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/cliharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcatalogtrust"
	"github.com/yeomyeonggeori/blueclaw/internal/turnoutcome"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const (
	BundledACPHarnessName  = "bluecollar-acp"
	ExternalHarnessName    = "acp"
	ClaudeCodeHarnessName  = "claude-code"
	CodexHarnessName       = "codex"
	AntigravityHarnessName = "antigravity"
)

var externalAgentToolCatalogTrust = toolcatalogtrust.Trust{
	SessionMeta: map[string]any{"claudeCode": map[string]any{"options": map[string]any{"allowedTools": []string{"mcp__" + acpharness.ToolCatalogServerName}}}},
}

type ToolCatalogEndpoint struct {
	URL          string
	Resolver     *mcpserver.SessionTokenRequesterResolver
	Handler      http.Handler
	ApprovalGate *approvalgate.Gate

	BridgeCommandPath string
}

type RequesterProcessRunner interface {
	Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error)
}

type SandboxProcessBoundary struct {
	Runner            RequesterProcessRunner
	WorkspaceRootPath string
}

type Option func(*selectionConfiguration)

type selectionConfiguration struct {
	bundledACPFactory harnessdriver.ACPFactory
}

func WithBundledACPFactory(bundledACPFactory harnessdriver.ACPFactory) Option {
	return func(configuration *selectionConfiguration) { configuration.bundledACPFactory = bundledACPFactory }
}

func Select(harnessConfiguration config.HarnessConfiguration, toolCatalogEndpoint ToolCatalogEndpoint, processBoundary SandboxProcessBoundary, options ...Option) (harnessdriver.Factory, error) {
	configuration := selectionConfiguration{}
	for _, option := range options {
		option(&configuration)
	}
	harnessName := strings.TrimSpace(harnessConfiguration.Name)
	switch harnessName {
	case "", BundledACPHarnessName:
		return bundledACPHarnessFactory(configuration.bundledACPFactory, toolCatalogEndpoint)
	case ExternalHarnessName:
		return externalHarnessFactory(harnessConfiguration, toolCatalogEndpoint, processBoundary)
	}
	commandHarness, isKnown := commandHarnessNamed(harnessName)
	if !isKnown {
		return nil, fmt.Errorf("unknown harness %q; known harnesses are %s", harnessName, strings.Join(knownHarnessNames(), ", "))
	}
	return commandHarnessFactory(commandHarness, harnessConfiguration, toolCatalogEndpoint, processBoundary)
}

type CommandHarness struct {
	Name           string
	ExecutableName string
	agentCommand   func(commandPath string) cliharness.AgentCommand
}

var commandHarnesses = []CommandHarness{
	{Name: ClaudeCodeHarnessName, ExecutableName: "claude", agentCommand: cliharness.ClaudeCodeAgentCommand},
	{Name: CodexHarnessName, ExecutableName: "codex", agentCommand: cliharness.CodexAgentCommand},
	{Name: AntigravityHarnessName, ExecutableName: "agy", agentCommand: cliharness.AntigravityAgentCommand},
}

func CommandHarnesses() []CommandHarness {
	return append([]CommandHarness{}, commandHarnesses...)
}

func commandHarnessNamed(harnessName string) (CommandHarness, bool) {
	for _, commandHarness := range commandHarnesses {
		if commandHarness.Name == harnessName {
			return commandHarness, true
		}
	}
	return CommandHarness{}, false
}

func (commandHarness CommandHarness) agentCommandAt(commandPath string) cliharness.AgentCommand {
	agentCommand := commandHarness.agentCommand(commandPath)
	agentCommand.HarnessName = commandHarness.Name
	return agentCommand
}

func knownHarnessNames() []string {
	names := []string{BundledACPHarnessName, ExternalHarnessName}
	for _, commandHarness := range commandHarnesses {
		names = append(names, commandHarness.Name)
	}
	return names
}

func externalHarnessFactory(harnessConfiguration config.HarnessConfiguration, toolCatalogEndpoint ToolCatalogEndpoint, processBoundary SandboxProcessBoundary) (harnessdriver.Factory, error) {
	if strings.TrimSpace(harnessConfiguration.AgentCommandPath) == "" {
		return nil, fmt.Errorf("harness %q needs agent.harness.agentCommandPath, the ACP agent to run", ExternalHarnessName)
	}
	if toolCatalogEndpoint.Resolver == nil || strings.TrimSpace(toolCatalogEndpoint.URL) == "" {
		return nil, fmt.Errorf("harness %q needs a published tool catalog; without one the agent would have no tools it may run as the requester", ExternalHarnessName)
	}
	if processBoundary.Runner == nil {
		return nil, fmt.Errorf("harness %q may only run inside the requester's POSIX identity, because it brings tools of its own that the kernel rather than a deny list has to confine; configure the terminal boundary first", ExternalHarnessName)
	}
	agentCommand := acpharness.AgentCommand{
		Path:      harnessConfiguration.AgentCommandPath,
		Arguments: append([]string{}, harnessConfiguration.AgentArguments...),
	}
	publisher := grantingPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver}
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		harness := acpharness.New(agentCommand, publisher, dependencies.TaskRunStore)
		harness.UseToolCatalogBridge(toolCatalogEndpoint.BridgeCommandPath)
		harness.UseToolCatalogTrust(externalAgentToolCatalogTrust)
		if toolCatalogEndpoint.ApprovalGate != nil {
			harness.UsePermissionAsker(toolCatalogEndpoint.ApprovalGate)
		}
		harness.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
		harness.UseRequesterProcessRunner(processBoundary.Runner, processBoundary.WorkspaceRootPath)
		harness.UseOutcomeClassifier(turnoutcome.NewClassifier(dependencies.IntakeLanguageModelProvider))
		return harness, nil
	}, nil
}

func commandHarnessFactory(commandHarness CommandHarness, harnessConfiguration config.HarnessConfiguration, toolCatalogEndpoint ToolCatalogEndpoint, processBoundary SandboxProcessBoundary) (harnessdriver.Factory, error) {
	harnessName := commandHarness.Name
	if strings.TrimSpace(harnessConfiguration.AgentCommandPath) == "" {
		return nil, fmt.Errorf("harness %q needs agent.harness.agentCommandPath, the executable to run", harnessName)
	}
	if toolCatalogEndpoint.Resolver == nil || strings.TrimSpace(toolCatalogEndpoint.URL) == "" {
		return nil, fmt.Errorf("harness %q needs a published tool catalog; without one the agent would have no tools it may run as the requester", harnessName)
	}
	if processBoundary.Runner == nil {
		return nil, fmt.Errorf("harness %q may only run inside the requester's POSIX identity, because it brings tools of its own that the kernel rather than a deny list has to confine; configure the terminal boundary first", harnessName)
	}
	agentCommand := commandHarness.agentCommandAt(strings.TrimSpace(harnessConfiguration.AgentCommandPath))
	publisher := grantingPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver}
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		harness := cliharness.New(agentCommand, publisher, dependencies.TaskRunStore)
		harness.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
		harness.UseOutcomeClassifier(turnoutcome.NewClassifier(dependencies.IntakeLanguageModelProvider))
		harness.UseRequesterProcessRunner(processBoundary.Runner, processBoundary.WorkspaceRootPath)
		return harness, nil
	}, nil
}

func bundledACPHarnessFactory(bundledACPFactory harnessdriver.ACPFactory, toolCatalogEndpoint ToolCatalogEndpoint) (harnessdriver.Factory, error) {
	if bundledACPFactory == nil {
		return nil, fmt.Errorf("harness %q is not part of this build; set agent.harness.name to %q with an agent command", BundledACPHarnessName, ExternalHarnessName)
	}
	if toolCatalogEndpoint.Resolver == nil || strings.TrimSpace(toolCatalogEndpoint.URL) == "" {
		return nil, fmt.Errorf("harness %q needs a published tool catalog; without one the agent would have no tools to run as the requester", BundledACPHarnessName)
	}
	return bundledACPFactory(grantingPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver}), nil
}

type grantingPublisher struct {
	endpointURL string
	resolver    *mcpserver.SessionTokenRequesterResolver
}

func (publisher grantingPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	sessionToken, errorValue := publisher.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpointURL, sessionToken, func() { publisher.resolver.RevokeSessionToken(sessionToken) }, nil
}
