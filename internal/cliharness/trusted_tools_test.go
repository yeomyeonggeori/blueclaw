package cliharness

import (
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestClaudeCodeIsLaunchedTrustingExactlyBlueclawsServer(t *testing.T) {
	arguments := ClaudeCodeAgentCommand("claude").ToolCatalogTrust.Arguments

	if !slices.Equal(arguments, []string{"--allowedTools", "mcp__blueclaw"}) {
		t.Fatalf("expected --allowedTools mcp__blueclaw, got %v", arguments)
	}
}

func TestCodexIsLaunchedApprovingBlueclawsServerTools(t *testing.T) {
	command := CodexAgentCommand("codex")
	arguments := command.ToolCatalogInlineArguments("http://localhost/mcp", "TOKEN_ENVIRONMENT", command.ToolCatalogTrust.ServerSettings)

	if len(arguments) != 2 || !strings.HasPrefix(arguments[1], "mcp_servers.blueclaw={") || !strings.Contains(arguments[1], `default_tools_approval_mode="approve"`) {
		t.Fatalf("expected the blueclaw server to be configured with approved tools, got %v", arguments)
	}
}

func TestCodexIsLaunchedWithTheDeclaredServerTrust(t *testing.T) {
	harness, _ := sessionTestHarness(t, CodexAgentCommand(writeFakeCodexScript(t)))

	output := runEchoingTurn(t, harness, agentcontract.AgentTurnRequest{RequesterPersonID: "person-1", ExistingTaskRunID: "task-run-1", Prompt: "hello"})

	if !strings.Contains(output, `default_tools_approval_mode="approve"`) {
		t.Fatalf("expected the launched codex to be handed the declared trust, got %q", output)
	}
}
