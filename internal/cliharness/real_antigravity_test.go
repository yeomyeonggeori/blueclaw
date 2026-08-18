package cliharness

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestRealAntigravityCallsADaemonTool(t *testing.T) {
	commandPath := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_ANTIGRAVITY_PATH"))
	if commandPath == "" {
		resolvedPath, errorValue := exec.LookPath("agy")
		if errorValue != nil {
			t.Skip("agy is not installed, so antigravity cannot be driven here")
		}
		commandPath = resolvedPath
	}

	execution := &daemonToolExecution{}
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token-antigravity" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)
	publisher := &catalogPublisher{endpointURL: catalogServer.URL, resolver: resolver}

	harness := New(runningAsTheDeveloper(AntigravityAgentCommand(commandPath)), publisher, nil)

	turnContext, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
