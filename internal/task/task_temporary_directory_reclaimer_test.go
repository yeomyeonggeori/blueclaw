package task

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func newReclaimingTaskRunService(t *testing.T) (*TaskRunService, string) {
	t.Helper()
	taskRunService, workspaceRootPath, _ := newReclaimingTaskRunServiceWithOwners(t)
	return taskRunService, workspaceRootPath
}

func newReclaimingTaskRunServiceWithOwners(t *testing.T) (*TaskRunService, string, *ownerActors) {
	t.Helper()
	workspaceRootPath := t.TempDir()
	owners := &ownerActors{}
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRunService.RegisterTaskRunTransitionObserver(NewTaskTemporaryDirectoryReclaimer(workspaceRootPath, owners, nil).Observe)
	return taskRunService, workspaceRootPath, owners
}

// ownerActors stands in for the POSIX helper: it records whose identity a
// command ran as and carries out a removal the way rm would.
type ownerActors struct {
	security.WorkspaceActor
	ranAs []string
	ran   [][]string
}

func (owners *ownerActors) Requester(_ context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	owners.ranAs = append(owners.ranAs, request.PersonAccess.PersonID)
	return owners, nil
}

func (owners *ownerActors) CanListDirectory(context.Context) bool { return false }

func (owners *ownerActors) Run(_ context.Context, request security.CommandRequest) (security.CommandResult, error) {
	owners.ran = append(owners.ran, append([]string{request.ExecutableName}, request.Arguments...))
	if request.ExecutableName != "rm" || len(request.Arguments) != 3 {
		return security.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, nil
	}
	if errorValue := os.RemoveAll(request.Arguments[2]); errorValue != nil {
		return security.CommandResult{ExitCode: 1, Stderr: errorValue.Error()}, nil
	}
	return security.CommandResult{}, nil
}

func TestAFinishedTaskDirectoryIsRemovedAsThePersonItBelongsTo(t *testing.T) {
	taskRunService, workspaceRootPath, owners := newReclaimingTaskRunServiceWithOwners(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "dm:channel-1", "render the deck")
	taskTemporaryDirectoryPath := createTaskTemporaryDirectory(t, workspaceRootPath, "person-1", taskRun.TaskRunID)
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "default"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if !slices.Equal(owners.ranAs, []string{"person-1"}) {
		t.Fatalf("the directory was removed as %v, not as the person whose home holds it", owners.ranAs)
	}
	if len(owners.ran) != 1 || !slices.Equal(owners.ran[0], []string{"rm", "-rf", "--", taskTemporaryDirectoryPath}) {
		t.Fatalf("the removal ran %v", owners.ran)
	}
}

func createTaskTemporaryDirectory(t *testing.T, workspaceRootPath string, requesterPersonID string, taskRunID string) string {
	t.Helper()
	requesterHomePath := security.PersonHomeDirectoryPath(workspaceRootPath, requesterPersonID)
	taskTemporaryDirectoryPath := security.TaskTemporaryDirectoryPath(requesterHomePath, taskRunID)
	if errorValue := os.MkdirAll(filepath.Join(taskTemporaryDirectoryPath, "tmp"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(taskTemporaryDirectoryPath, "tmp", "terminal-output"), []byte("spilled"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	return taskTemporaryDirectoryPath
}

func expectTaskTemporaryDirectoryRemoved(t *testing.T, taskTemporaryDirectoryPath string) {
	t.Helper()
	if _, errorValue := os.Stat(taskTemporaryDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected task tmp %s to be removed, got %v", taskTemporaryDirectoryPath, errorValue)
	}
}

func TestCompletedTaskRunRemovesItsTemporaryDirectory(t *testing.T) {
	taskRunService, workspaceRootPath := newReclaimingTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "dm:channel-1", "render the deck")
	taskTemporaryDirectoryPath := createTaskTemporaryDirectory(t, workspaceRootPath, "person-1", taskRun.TaskRunID)
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "default"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatal(errorValue)
	}

	expectTaskTemporaryDirectoryRemoved(t, taskTemporaryDirectoryPath)
}

func TestFailedTaskRunRemovesItsTemporaryDirectory(t *testing.T) {
	taskRunService, workspaceRootPath := newReclaimingTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "dm:channel-1", "render the deck")
	taskTemporaryDirectoryPath := createTaskTemporaryDirectory(t, workspaceRootPath, "person-1", taskRun.TaskRunID)
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "default"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "render_failed"); errorValue != nil {
		t.Fatal(errorValue)
	}

	expectTaskTemporaryDirectoryRemoved(t, taskTemporaryDirectoryPath)
}

func TestCancelledTaskRunRemovesItsTemporaryDirectory(t *testing.T) {
	taskRunService, workspaceRootPath := newReclaimingTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "dm:channel-1", "render the deck")
	taskTemporaryDirectoryPath := createTaskTemporaryDirectory(t, workspaceRootPath, "person-1", taskRun.TaskRunID)
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "default"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := taskRunService.CancelTaskRunWithReason(taskRun.TaskRunID, "person-1", "superseded_by_new_message"); errorValue != nil {
		t.Fatal(errorValue)
	}

	expectTaskTemporaryDirectoryRemoved(t, taskTemporaryDirectoryPath)
}

func TestExpiredBlockedTaskRunRemovesItsTemporaryDirectoryOnFailure(t *testing.T) {
	taskRunService, workspaceRootPath := newReclaimingTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "dm:channel-1", "render the deck")
	taskTemporaryDirectoryPath := createTaskTemporaryDirectory(t, workspaceRootPath, "person-1", taskRun.TaskRunID)
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "default"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, TaskStatusBlocked, "max_elapsed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := os.Stat(taskTemporaryDirectoryPath); errorValue != nil {
		t.Fatalf("expected a resumable blocked task run to keep its tmp, got %v", errorValue)
	}

	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "blocked_expired"); errorValue != nil {
		t.Fatal(errorValue)
	}

	expectTaskTemporaryDirectoryRemoved(t, taskTemporaryDirectoryPath)
}

func TestWaitingTaskRunKeepsItsTemporaryDirectory(t *testing.T) {
	taskRunService, workspaceRootPath := newReclaimingTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "dm:channel-1", "render the deck")
	taskTemporaryDirectoryPath := createTaskTemporaryDirectory(t, workspaceRootPath, "person-1", taskRun.TaskRunID)
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "default"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, TaskStatusWaitingApproval, "approval_pending"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := os.Stat(taskTemporaryDirectoryPath); errorValue != nil {
		t.Fatalf("expected a task waiting for approval to keep its tmp, got %v", errorValue)
	}
}

func TestInterruptedTaskRunKeepsItsTemporaryDirectoryUntilItFails(t *testing.T) {
	taskRunService, workspaceRootPath := newReclaimingTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "dm:channel-1", "render the deck")
	taskTemporaryDirectoryPath := createTaskTemporaryDirectory(t, workspaceRootPath, "person-1", taskRun.TaskRunID)

	if _, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, TaskInterruptReasonRuntimeRestart); !isInterrupted {
		t.Fatal("expected the inactive task run to be interrupted")
	}
	if _, errorValue := os.Stat(taskTemporaryDirectoryPath); errorValue != nil {
		t.Fatalf("expected a resumable interrupted task run to keep its tmp, got %v", errorValue)
	}

	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "interrupted_not_resumed"); errorValue != nil {
		t.Fatal(errorValue)
	}

	expectTaskTemporaryDirectoryRemoved(t, taskTemporaryDirectoryPath)
}
