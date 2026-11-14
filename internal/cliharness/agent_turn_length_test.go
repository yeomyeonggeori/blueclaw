package cliharness

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type recordingRequesterActor struct {
	commandRequests []security.CommandRequest
}

func (actor *recordingRequesterActor) Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	return actor, nil
}

func (actor *recordingRequesterActor) Run(_ context.Context, commandRequest security.CommandRequest) (security.CommandResult, error) {
	actor.commandRequests = append(actor.commandRequests, commandRequest)
	return security.CommandResult{Stdout: "done"}, nil
}

func (actor *recordingRequesterActor) MkdirAll(context.Context, string) error { return nil }

func (actor *recordingRequesterActor) WriteFile(context.Context, string, []byte) error { return nil }

func (actor *recordingRequesterActor) ReadFile(context.Context, string, int64) ([]byte, error) {
	return nil, nil
}

func (actor *recordingRequesterActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, nil
}

func (actor *recordingRequesterActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, nil
}

func TestACommandLineAgentRunsAsAHarnessAgentRatherThanAShellCommand(t *testing.T) {
	harness, _ := sessionTestHarness(t, AgentCommand{Path: "/bin/echo", HarnessName: "test-harness"})
	requesterActor := &recordingRequesterActor{}
	harness.UseRequesterProcessRunner(requesterActor, t.TempDir())

	runEchoingTurn(t, harness, agentcontract.AgentTurnRequest{RequesterIdentity: agentcontract.RequesterIdentity{RequesterPersonID: "person-1"}, Prompt: "턴"})

	if len(requesterActor.commandRequests) != 1 || !requesterActor.commandRequests[0].IsHarnessAgent {
		t.Fatalf("a whole agent turn is not held to the terminal's limit for one command, got %+v", requesterActor.commandRequests)
	}
}
