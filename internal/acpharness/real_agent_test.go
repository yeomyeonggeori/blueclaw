package acpharness

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestARealACPAgentCallsADaemonTool(t *testing.T) {
	commandPath := strings.TrimSpace(os.Getenv("BLUECLAW_TEST_ACP_AGENT_PATH"))
	if commandPath == "" {
		t.Skip("set BLUECLAW_TEST_ACP_AGENT_PATH to an ACP agent command to drive a real harness here")
	}

	agentArguments := strings.Fields(os.Getenv("BLUECLAW_TEST_ACP_AGENT_ARGUMENTS"))

	executed := []daemonExecutedTool{}
	toolCatalog := newPublishedToolCatalog(t)
	harness := New(AgentCommand{Path: commandPath, Arguments: agentArguments, Environment: os.Environ()}, toolCatalog, nil)

	turnContext, cancelTurn := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelTurn()
	turnResult, errorValue := harness.RunTurn(turnContext, agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		Prompt:            "Call the note_write tool with text \"acp-agent-ok\". Do not use any other tool. Then reply with only the word done.",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatalf("expected the agent to run a turn over ACP: %v", errorValue)
	}
	if len(executed) != 1 || executed[0].toolName != "note_write" || executed[0].requesterPersonID != "person-1" {
		t.Fatalf("expected the agent to reach the requester's tool catalog and the daemon to execute the tool, got %+v", executed)
	}
	if turnResult.TaskRun.Status != "completed" {
		t.Fatalf("expected a completed turn, got %+v", turnResult.TaskRun)
	}
	if toolCatalog.revokeCount != 1 {
		t.Fatalf("expected the tool catalog session to be revoked after the turn, got %d", toolCatalog.revokeCount)
	}
}
