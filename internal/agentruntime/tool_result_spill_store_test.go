package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type spillWorkspaceActor struct {
	writtenPath      string
	writtenContent   []byte
	createdDirectory string
	writeError       error
}

func (actor *spillWorkspaceActor) Run(context.Context, security.CommandRequest) (security.CommandResult, error) {
	return security.CommandResult{}, nil
}

func (actor *spillWorkspaceActor) MkdirAll(_ context.Context, directoryPath string) error {
	actor.createdDirectory = directoryPath
	return nil
}

func (actor *spillWorkspaceActor) WriteFile(_ context.Context, filePath string, content []byte) error {
	if actor.writeError != nil {
		return actor.writeError
	}
	actor.writtenPath = filePath
	actor.writtenContent = content
	return nil
}

func (actor *spillWorkspaceActor) ReadFile(context.Context, string, int64) ([]byte, error) {
	return nil, nil
}

func (actor *spillWorkspaceActor) BundleDirectory(context.Context, string, security.WorkspaceActorBundleOptions) (security.WorkspaceActorBundle, error) {
	return security.WorkspaceActorBundle{}, nil
}

func (actor *spillWorkspaceActor) ListDirectory(context.Context, string) ([]security.WorkspaceActorDirectoryEntry, error) {
	return nil, nil
}

func (actor *spillWorkspaceActor) Stat(context.Context, string) (security.WorkspaceActorStat, error) {
	return security.WorkspaceActorStat{}, nil
}

type spillWorkspaceActorFactory struct {
	actor            *spillWorkspaceActor
	requestedPersons []string
}

func (factory *spillWorkspaceActorFactory) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	factory.requestedPersons = append(factory.requestedPersons, request.PersonAccess.PersonID)
	return factory.actor, nil
}

func (factory *spillWorkspaceActorFactory) CanListDirectory(context.Context) bool { return true }

func spillStoreForTest(t *testing.T) (*RequesterToolResultSpillStore, *spillWorkspaceActorFactory, taskstate.TaskRun) {
	t.Helper()
	taskRunService := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRunWithOrigin("person-7", taskstate.TaskRunOrigin{}, "read the build log")
	factory := &spillWorkspaceActorFactory{actor: &spillWorkspaceActor{}}
	return NewRequesterToolResultSpillStore(factory, taskRunService), factory, taskRun
}

func TestASpillIsWrittenAsThePersonWhoAskedForTheWork(t *testing.T) {
	store, factory, taskRun := spillStoreForTest(t)

	spillRef, errorValue := store.SaveToolResultSpill(context.Background(), ToolResultSpill{
		TaskRunID:         taskRun.TaskRunID,
		ObservationID:     "obs-4",
		ToolName:          "shell",
		WorkspaceRootPath: "/workspace",
		SuggestedName:     "shell.result.txt",
		Content:           "the whole build log",
	})

	if errorValue != nil {
		t.Fatalf("expected the spill to be written: %v", errorValue)
	}
	if len(factory.requestedPersons) != 1 || factory.requestedPersons[0] != "person-7" {
		t.Fatalf("a file the agent will read has to be written by the identity that may read it, got %v", factory.requestedPersons)
	}
	if string(factory.actor.writtenContent) != "the whole build log" {
		t.Fatalf("the point of the spill is the part the prompt dropped, so it is written whole, got %q", factory.actor.writtenContent)
	}
	if spillRef.Locator != factory.actor.writtenPath {
		t.Fatalf("the agent is told to read %q but the file went to %q", spillRef.Locator, factory.actor.writtenPath)
	}
	if spillRef.Bytes != len("the whole build log") {
		t.Fatalf("an agent deciding whether to read it needs its size, got %d", spillRef.Bytes)
	}
	if strings.TrimSpace(spillRef.RetrievalHint) == "" {
		t.Fatal("a path with no way to read it is not a retrieval")
	}
}

func TestASpillLivesInTheDirectoryTheTaskAlreadyCleansUp(t *testing.T) {
	store, factory, taskRun := spillStoreForTest(t)

	_, errorValue := store.SaveToolResultSpill(context.Background(), ToolResultSpill{
		TaskRunID:         taskRun.TaskRunID,
		ObservationID:     "obs-4",
		WorkspaceRootPath: "/workspace",
		SuggestedName:     "shell.result.txt",
		Content:           "output",
	})

	if errorValue != nil {
		t.Fatalf("expected the spill to be written: %v", errorValue)
	}
	expectedTaskDirectory := security.TaskTemporaryDirectoryPath(security.PersonHomeDirectoryPath("/workspace", "person-7"), taskRun.TaskRunID)
	if !strings.HasPrefix(factory.actor.createdDirectory, expectedTaskDirectory) {
		t.Fatalf("a spill outside the task temporary directory is never reclaimed and accumulates forever, got %q", factory.actor.createdDirectory)
	}
}

func TestASuggestedNameCannotSteerTheSpillOutOfItsDirectory(t *testing.T) {
	store, factory, taskRun := spillStoreForTest(t)

	_, errorValue := store.SaveToolResultSpill(context.Background(), ToolResultSpill{
		TaskRunID:         taskRun.TaskRunID,
		ObservationID:     "obs-4",
		WorkspaceRootPath: "/workspace",
		SuggestedName:     "../../../../etc/cron.d/payload",
		Content:           "output",
	})

	if errorValue != nil {
		t.Fatalf("expected the spill to be written: %v", errorValue)
	}
	if !strings.HasPrefix(factory.actor.writtenPath, factory.actor.createdDirectory) {
		t.Fatalf("a suggested name is a hint, never a path, got %q", factory.actor.writtenPath)
	}
}

func TestAStoreWithNoIdentityToWriteAsSaysSoRatherThanWritingAsItself(t *testing.T) {
	taskRunService := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRunWithOrigin("person-7", taskstate.TaskRunOrigin{}, "read the build log")
	store := NewRequesterToolResultSpillStore(nil, taskRunService)

	_, errorValue := store.SaveToolResultSpill(context.Background(), ToolResultSpill{
		TaskRunID:         taskRun.TaskRunID,
		WorkspaceRootPath: "/workspace",
		Content:           "output",
	})

	if errorValue == nil {
		t.Fatal("writing the spill as the daemon would put a file the requester cannot read where the agent was told to look")
	}
}

func TestAWriteFailureIsReportedRatherThanReturningAPathWithNoFile(t *testing.T) {
	store, factory, taskRun := spillStoreForTest(t)
	factory.actor.writeError = errors.New("no space left on device")

	spillRef, errorValue := store.SaveToolResultSpill(context.Background(), ToolResultSpill{
		TaskRunID:         taskRun.TaskRunID,
		WorkspaceRootPath: "/workspace",
		Content:           "output",
	})

	if errorValue == nil {
		t.Fatal("a locator pointing at a file that was never written sends the agent to read nothing")
	}
	if spillRef.Locator != "" {
		t.Fatalf("a failed save has no locator, got %q", spillRef.Locator)
	}
}
