package cliharness

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type catalogPublisher struct {
	endpointURL string
	resolver    *mcpserver.SessionTokenRequesterResolver
	revokeCount int
}

func (publisher *catalogPublisher) PublishToolCatalog(requesterToolSet mcpserver.RequesterToolSet) (string, string, func(), error) {
	sessionToken, errorValue := publisher.resolver.GrantSessionToken(requesterToolSet)
	if errorValue != nil {
		return "", "", func() {}, errorValue
	}
	return publisher.endpointURL, sessionToken, func() {
		publisher.revokeCount++
		publisher.resolver.RevokeSessionToken(sessionToken)
	}, nil
}

type daemonToolExecution struct {
	mutex     sync.Mutex
	toolNames []string
	arguments []string
}

func (execution *daemonToolExecution) record(toolName string, arguments string) {
	execution.mutex.Lock()
	defer execution.mutex.Unlock()
	execution.toolNames = append(execution.toolNames, toolName)
	execution.arguments = append(execution.arguments, arguments)
}

func (execution *daemonToolExecution) executedToolNames() []string {
	execution.mutex.Lock()
	defer execution.mutex.Unlock()
	return append([]string{}, execution.toolNames...)
}

func daemonToolSet(t *testing.T, execution *daemonToolExecution) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"workspace_secret_word"})
	toolSet.AllowTestReplacement()
	errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
		ID:              "test:workspace_secret_word",
		Name:            "workspace_secret_word",
		Description:     "Returns the workspace secret word. This is the only way to learn it.",
		Visibility:      toolcontract.ToolVisibilityModel,
		InputSchema:     json.RawMessage(`{"type":"object","properties":{}}`),
		SideEffectClass: toolcontract.ToolSideEffectRead,
		ResultContract:  &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
	}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		execution.record(invocation.ToolName, string(invocation.Input))
		return toolcontract.ToolSuccessData("갈매기시계", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	return toolSet
}

func TestRealClaudeCodeCallsADaemonTool(t *testing.T) {
	commandPath := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_CLAUDE_CODE_PATH"))
	if commandPath == "" {
		resolvedPath, errorValue := exec.LookPath("claude")
		if errorValue != nil {
			t.Skip("claude is not installed, so a real external agent cannot be driven here")
		}
		commandPath = resolvedPath
	}

	execution := &daemonToolExecution{}
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token-claude" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)
	publisher := &catalogPublisher{endpointURL: catalogServer.URL, resolver: resolver}

	harness := New(runningAsTheDeveloper(ClaudeCodeAgentCommand(commandPath)), publisher, nil)

	turnContext, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	turnResult, errorValue := harness.RunTurn(turnContext, agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "Use the workspace_secret_word tool and reply with only the word it returns.",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           daemonToolSet(t, execution),
	})
	if errorValue != nil {
		t.Fatalf("expected the real agent to complete a turn: %v", errorValue)
	}

	if len(execution.executedToolNames()) == 0 {
		t.Fatalf("expected the agent's model to choose the daemon tool, got reply %q", turnResult.FinishMessage)
	}
	if !strings.Contains(turnResult.FinishMessage, "갈매기시계") {
		t.Fatalf("expected the daemon tool's output to reach the reply, got %q", turnResult.FinishMessage)
	}
	if publisher.revokeCount != 1 {
		t.Fatalf("expected the catalog grant to be revoked when the turn ended, got %d", publisher.revokeCount)
	}
}

func TestRealCodexCallsADaemonTool(t *testing.T) {
	commandPath := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_CODEX_PATH"))
	if commandPath == "" {
		resolvedPath, errorValue := exec.LookPath("codex")
		if errorValue != nil {
			t.Skip("codex is not installed, so it cannot be driven here")
		}
		commandPath = resolvedPath
	}

	execution := &daemonToolExecution{}
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token-codex" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)
	publisher := &catalogPublisher{endpointURL: catalogServer.URL, resolver: resolver}

	harness := New(runningAsTheDeveloper(CodexAgentCommand(commandPath)), publisher, nil)

	turnContext, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	turnResult, errorValue := harness.RunTurn(turnContext, agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "Call the workspace_secret_word tool and reply with only the word it returns.",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           daemonToolSet(t, execution),
	})
	if errorValue != nil {
		t.Fatalf("expected codex to complete a turn: %v", errorValue)
	}
	if len(execution.executedToolNames()) == 0 {
		t.Fatalf("expected codex's model to choose the daemon tool, got reply %q", turnResult.FinishMessage)
	}
	if !strings.Contains(turnResult.FinishMessage, "갈매기시계") {
		t.Fatalf("expected the daemon tool's output to reach the reply, got %q", turnResult.FinishMessage)
	}
}

// runningAsTheDeveloper hands the agent CLI this machine's own environment, which is
// how a developer runs Claude Code or Codex against their own credentials. A turn that
// runs inside the requester's POSIX identity builds its environment from that identity
// instead and never passes through here.
func runningAsTheDeveloper(agentCommand AgentCommand) AgentCommand {
	agentCommand.Environment = os.Environ()
	return agentCommand
}
