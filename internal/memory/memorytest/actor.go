package memorytest

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

// ReadsInThisProcess is the actor for a harness that has no POSIX helper to
// switch identity with, such as a test or a scripted session. A shipped
// binary never builds one: it takes the helper's factory, and the kernel is
// what refuses a file.
func ReadsInThisProcess() security.WorkspaceActorFactory { return currentProcessActor{} }

// currentProcessActor serves a read in the test's own process. A test binary
// carries no memory-read subcommand to exec, so this stands in for the
// identity switch and nothing else: everything a read does to a file is the
// same code the helper would run.
type currentProcessActor struct{}

func (currentProcessActor) Requester(context.Context, security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	return currentProcessActor{}, nil
}

func (currentProcessActor) CanListDirectory(context.Context) bool { return false }

func (currentProcessActor) Run(ctx context.Context, request security.CommandRequest) (security.CommandResult, error) {
	var output bytes.Buffer
	if errorValue := memory.RunRead(ctx, strings.NewReader(request.Stdin), &output); errorValue != nil {
		return security.CommandResult{ExitCode: 1, Stderr: errorValue.Error()}, nil
	}
	return security.CommandResult{Stdout: output.String()}, nil
}

func (currentProcessActor) MkdirAll(context.Context, string) error {
	return errors.New("a read creates nothing")
}

func (currentProcessActor) WriteFile(context.Context, string, []byte) error {
	return errors.New("a read writes nothing")
}

func (currentProcessActor) ReadFile(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("a read goes through Run")
}

func (currentProcessActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, errors.New("a read lists nothing")
}

func (currentProcessActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, errors.New("a read stats nothing")
}
