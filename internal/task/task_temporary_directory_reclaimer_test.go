package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func newReclaimingTaskRunService(t *testing.T) (*TaskRunService, string) {
	t.Helper()
	workspaceRootPath := t.TempDir()
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRunService.RegisterTaskRunTransitionObserver(NewTaskTemporaryDirectoryReclaimer(workspaceRootPath, nil).Observe)
	return taskRunService, workspaceRootPath
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
