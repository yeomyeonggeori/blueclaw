package harnessselection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/acpharness"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func bundledFactory() harnessdriver.Factory {
	return func(harnessdriver.Dependencies) (agentcontract.Harness, agentcontract.SkillRetriever) {
		return nil, nil
	}
}

func publishedCatalog() ToolCatalogEndpoint {
	return ToolCatalogEndpoint{URL: "http://127.0.0.1:0/tools", Resolver: mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })}
}

func TestSelectNamesTheBundledAgentOverACPWhenNothingIsConfigured(t *testing.T) {
	servedOverACP := false
	acpFactory := func(toolCatalogPublisher acpharness.ToolCatalogPublisher) harnessdriver.Factory {
		servedOverACP = true
		return bundledFactory()
	}

	selectedFactory, errorValue := Select(config.HarnessConfiguration{}, publishedCatalog(), SandboxProcessBoundary{}, WithBundledACPFactory(acpFactory))

	if errorValue != nil || selectedFactory == nil || !servedOverACP {
		t.Fatalf("an unconfigured harness is the bundled agent over ACP, got served=%v %v", servedOverACP, errorValue)
	}
}

func TestSelectFailsLoudlyWhenNoHarnessIsAvailable(t *testing.T) {
	_, errorValue := Select(config.HarnessConfiguration{}, publishedCatalog(), SandboxProcessBoundary{})
	if errorValue == nil || !strings.Contains(errorValue.Error(), ExternalHarnessName) {
		t.Fatalf("expected a build with no bundled harness to say how to configure one, got %v", errorValue)
	}
}

func TestSelectRejectsAnUnknownHarnessRatherThanIgnoringIt(t *testing.T) {
	_, errorValue := Select(config.HarnessConfiguration{Name: "claude-code"}, publishedCatalog(), SandboxProcessBoundary{})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "claude-code") {
		t.Fatalf("expected an unknown harness name to be refused by name, got %v", errorValue)
	}
}

func TestSelectRefusesAnExternalHarnessThatWouldHaveNoToolsOrNoAgent(t *testing.T) {
	if _, errorValue := Select(config.HarnessConfiguration{Name: ExternalHarnessName}, publishedCatalog(), SandboxProcessBoundary{}); errorValue == nil {
		t.Fatal("expected an external harness with no agent command to be refused")
	}
	_, errorValue := Select(config.HarnessConfiguration{Name: ExternalHarnessName, AgentCommandPath: "/usr/bin/true"}, ToolCatalogEndpoint{}, SandboxProcessBoundary{})
	if errorValue == nil {
		t.Fatal("expected an external harness with no published tool catalog to be refused, because it would have no tools it may run as the requester")
	}
}

func TestSelectBuildsTheExternalHarnessWhenBothAreConfigured(t *testing.T) {
	selectedFactory, errorValue := Select(config.HarnessConfiguration{Name: ExternalHarnessName, AgentCommandPath: "/usr/bin/true"}, publishedCatalog(), SandboxProcessBoundary{Runner: refusingProcessRunner{}, WorkspaceRootPath: "/workspace"})
	if errorValue != nil {
		t.Fatalf("expected a configured external harness: %v", errorValue)
	}
	harness, _ := selectedFactory(harnessdriver.Dependencies{})
	if harness == nil {
		t.Fatal("expected the external harness to be constructed")
	}
}

func TestPublishedCatalogGrantsAndRevokesPerTurn(t *testing.T) {
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	publisher := sessionTokenPublisher{endpointURL: "http://127.0.0.1:0/tools", resolver: resolver}

	_, sessionToken, revoke, errorValue := publisher.PublishToolCatalog(mcpserver.RequesterToolSet{RequesterPersonID: "person-1", ToolSet: emptyToolSet()})
	if errorValue != nil {
		t.Fatalf("expected a tool catalog grant: %v", errorValue)
	}
	if _, errorValue := resolver.ResolveRequester(sessionToken); errorValue != nil {
		t.Fatalf("expected the granted token to resolve: %v", errorValue)
	}
	revoke()
	if _, errorValue := resolver.ResolveRequester(sessionToken); errorValue == nil {
		t.Fatal("expected the token to stop resolving once the turn ended")
	}
}

func TestClaudeCodeIsSelectableAndDeniesItsOwnBuiltinTools(t *testing.T) {
	if _, errorValue := Select(config.HarnessConfiguration{Name: ClaudeCodeHarnessName}, publishedCatalog(), SandboxProcessBoundary{}); errorValue == nil {
		t.Fatal("expected claude-code with no executable path to be refused")
	}
	if _, errorValue := Select(config.HarnessConfiguration{Name: ClaudeCodeHarnessName, AgentCommandPath: "/usr/bin/true"}, ToolCatalogEndpoint{}, SandboxProcessBoundary{}); errorValue == nil {
		t.Fatal("expected claude-code with no tool catalog to be refused")
	}
	if _, errorValue := Select(config.HarnessConfiguration{Name: ClaudeCodeHarnessName, AgentCommandPath: "/usr/bin/true"}, publishedCatalog(), SandboxProcessBoundary{}); errorValue == nil {
		t.Fatal("expected a cli harness with no POSIX boundary to be refused, because its own tools would run unconfined")
	}
	selectedFactory, errorValue := Select(config.HarnessConfiguration{Name: ClaudeCodeHarnessName, AgentCommandPath: "/usr/bin/true"}, publishedCatalog(), SandboxProcessBoundary{Runner: refusingProcessRunner{}, WorkspaceRootPath: "/workspace"})
	if errorValue != nil {
		t.Fatalf("expected a configured claude-code harness: %v", errorValue)
	}
	if harness, _ := selectedFactory(harnessdriver.Dependencies{}); harness == nil {
		t.Fatal("expected the claude-code harness to be constructed")
	}
}

func TestEveryHarnessThatBringsItsOwnToolsRequiresTheRequesterIdentityBoundary(t *testing.T) {
	for _, harnessName := range []string{ClaudeCodeHarnessName, CodexHarnessName, ExternalHarnessName} {
		if _, errorValue := Select(config.HarnessConfiguration{Name: harnessName, AgentCommandPath: "/usr/bin/true"}, publishedCatalog(), SandboxProcessBoundary{}); errorValue == nil {
			t.Fatalf("expected %q to be refused without the requester identity boundary", harnessName)
		}
	}
}

type refusingProcessRunner struct{}

func (refusingProcessRunner) Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	return nil, errors.New("not used in selection tests")
}
