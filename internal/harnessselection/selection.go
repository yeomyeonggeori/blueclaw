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
	"github.com/yeomyeonggeori/blueclaw/internal/turnoutcome"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	BundledHarnessName     = "bluecollar"
	ExternalHarnessName    = "acp"
	ClaudeCodeHarnessName  = "claude-code"
	CodexHarnessName       = "codex"
	AntigravityHarnessName = "antigravity"
)

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

func Select(harnessConfiguration config.HarnessConfiguration, bundledHarnessFactory harnessdriver.Factory, toolCatalogEndpoint ToolCatalogEndpoint, processBoundary SandboxProcessBoundary) (harnessdriver.Factory, error) {
	harnessName := strings.TrimSpace(harnessConfiguration.Name)
	switch harnessName {
	case "", BundledHarnessName:
		if bundledHarnessFactory == nil {
			return nil, fmt.Errorf("no harness is configured and this build ships none; set agent.harness.name to %q with an agent command", ExternalHarnessName)
		}
		return bundledHarnessFactory, nil
	case ExternalHarnessName:
		return externalHarnessFactory(harnessConfiguration, toolCatalogEndpoint, processBoundary)
	case ClaudeCodeHarnessName:
		return commandHarnessFactory(ClaudeCodeHarnessName, cliharness.ClaudeCodeAgentCommand(strings.TrimSpace(harnessConfiguration.AgentCommandPath)), harnessConfiguration, toolCatalogEndpoint, processBoundary)
	case CodexHarnessName:
		return commandHarnessFactory(CodexHarnessName, cliharness.CodexAgentCommand(strings.TrimSpace(harnessConfiguration.AgentCommandPath)), harnessConfiguration, toolCatalogEndpoint, processBoundary)
	case AntigravityHarnessName:
		return commandHarnessFactory(AntigravityHarnessName, cliharness.AntigravityAgentCommand(strings.TrimSpace(harnessConfiguration.AgentCommandPath)), harnessConfiguration, toolCatalogEndpoint, processBoundary)
	default:
		return nil, fmt.Errorf("unknown harness %q; known harnesses are %q, %q, %q, %q and %q", harnessName, BundledHarnessName, ExternalHarnessName, ClaudeCodeHarnessName, CodexHarnessName, AntigravityHarnessName)
	}
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
	publisher := sessionTokenPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver, approvalGate: toolCatalogEndpoint.ApprovalGate}
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		harness := acpharness.New(agentCommand, publisher, dependencies.TaskRunStore)
		harness.UseToolCatalogBridge(toolCatalogEndpoint.BridgeCommandPath)
		harness.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
		harness.UseRequesterProcessRunner(processBoundary.Runner, processBoundary.WorkspaceRootPath)
		harness.UseOutcomeClassifier(turnoutcome.NewClassifier(dependencies.IntakeLanguageModelProvider))
		return harness, nil
	}, nil
}

type sessionTokenPublisher struct {
	endpointURL  string
	resolver     *mcpserver.SessionTokenRequesterResolver
	approvalGate *approvalgate.Gate
}

func (publisher sessionTokenPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	requesterToolSet.ToolSet.UseToolCallGate(publisher.approvalGate.TurnGate(approvalgate.TurnContext{
		RequesterPersonID: requesterToolSet.RequesterPersonID,
		ResponseLanguage:  requesterToolSet.ResponseLanguage,
		Prompt:            requesterToolSet.Prompt,
		HarnessSession:    requesterToolSet.HarnessSession,
	}))
	sessionToken, errorValue := publisher.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpointURL, sessionToken, func() { publisher.resolver.RevokeSessionToken(sessionToken) }, nil
}

func commandHarnessFactory(harnessName string, agentCommand cliharness.AgentCommand, harnessConfiguration config.HarnessConfiguration, toolCatalogEndpoint ToolCatalogEndpoint, processBoundary SandboxProcessBoundary) (harnessdriver.Factory, error) {
	if strings.TrimSpace(harnessConfiguration.AgentCommandPath) == "" {
		return nil, fmt.Errorf("harness %q needs agent.harness.agentCommandPath, the executable to run", harnessName)
	}
	if toolCatalogEndpoint.Resolver == nil || strings.TrimSpace(toolCatalogEndpoint.URL) == "" {
		return nil, fmt.Errorf("harness %q needs a published tool catalog; without one the agent would have no tools it may run as the requester", harnessName)
	}
	if processBoundary.Runner == nil {
		return nil, fmt.Errorf("harness %q may only run inside the requester's POSIX identity, because it brings tools of its own that the kernel rather than a deny list has to confine; configure the terminal boundary first", harnessName)
	}
	publisher := sessionTokenPublisher{endpointURL: toolCatalogEndpoint.URL, resolver: toolCatalogEndpoint.Resolver, approvalGate: toolCatalogEndpoint.ApprovalGate}
	return func(dependencies harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		harness := cliharness.New(agentCommand, publisher, dependencies.TaskRunStore)
		harness.UseInstructionBundleLoader(dependencies.InstructionBundleLoader)
		harness.UseOutcomeClassifier(turnoutcome.NewClassifier(dependencies.IntakeLanguageModelProvider))
		if processBoundary.Runner != nil {
			harness.UseRequesterProcessRunner(processBoundary.Runner, processBoundary.WorkspaceRootPath)
		}
		return harness, nil
	}, nil
}
