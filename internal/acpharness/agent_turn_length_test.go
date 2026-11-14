package acpharness

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

type recordingProcessStarter struct {
	commandRequests []security.CommandRequest
}

func (starter *recordingProcessStarter) StartProcess(_ context.Context, commandRequest security.CommandRequest) (security.StreamingProcess, error) {
	starter.commandRequests = append(starter.commandRequests, commandRequest)
	return security.StreamingProcess{}, errors.New("recorded only")
}

func TestAnACPAgentRunsAsAHarnessAgentRatherThanAShellCommand(t *testing.T) {
	processStarter := &recordingProcessStarter{}

	_, _, _, _ = AgentCommand{Path: "/usr/local/bin/agent"}.StartAsRequester(context.Background(), processStarter, t.TempDir())

	if len(processStarter.commandRequests) != 1 || !processStarter.commandRequests[0].IsHarnessAgent {
		t.Fatalf("a whole agent turn is not held to the terminal's limit for one command, got %+v", processStarter.commandRequests)
	}
}
