package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskTemporaryDirectoryIsNotTheRequesterTemporaryDirectory(t *testing.T) {
	requesterHomePath := PersonHomeDirectoryPath("/workspace", "person-1")
	requesterTemporaryDirectoryPath := RequesterTemporaryDirectoryPath(requesterHomePath)
	taskTemporaryDirectoryPath := TaskTemporaryDirectoryPath(requesterHomePath, "task-run-1")

	if requesterTemporaryDirectoryPath != "/workspace/private/people/person-1/tmp" {
		t.Fatalf("expected person scoped requester tmp, got %s", requesterTemporaryDirectoryPath)
	}
	if taskTemporaryDirectoryPath != "/workspace/private/people/person-1/tmp/tasks/task-run-1" {
		t.Fatalf("expected task scoped tmp, got %s", taskTemporaryDirectoryPath)
	}
	if taskTemporaryDirectoryPath == requesterTemporaryDirectoryPath {
		t.Fatal("expected task tmp and requester tmp to be different directories")
	}
}

func TestTaskTemporaryDirectoryPathIsEmptyWithoutTaskRun(t *testing.T) {
	requesterHomePath := PersonHomeDirectoryPath("/workspace", "person-1")
	if taskTemporaryDirectoryPath := TaskTemporaryDirectoryPath(requesterHomePath, " "); taskTemporaryDirectoryPath != "" {
		t.Fatalf("expected no task tmp without a task run, got %s", taskTemporaryDirectoryPath)
	}
}

func TestTaskTemporaryDirectoryCleanerRemovesOnlyTheTaskDirectory(t *testing.T) {
	workspaceRootPath := t.TempDir()
	requesterHomePath := PersonHomeDirectoryPath(workspaceRootPath, "person-1")
	requesterTemporaryDirectoryPath := RequesterTemporaryDirectoryPath(requesterHomePath)
	taskTemporaryDirectoryPath := TaskTemporaryDirectoryPath(requesterHomePath, "task-run-1")
	if errorValue := os.MkdirAll(filepath.Join(taskTemporaryDirectoryPath, "tmp"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	spillPath := filepath.Join(taskTemporaryDirectoryPath, "tmp", "terminal-output-abc123")
	if errorValue := os.WriteFile(spillPath, []byte("spilled output"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}

	cleaner := TaskTemporaryDirectoryCleaner{WorkspaceRootPath: workspaceRootPath}
	if errorValue := cleaner.RemoveTaskTemporaryDirectory("person-1", "task-run-1"); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := os.Stat(taskTemporaryDirectoryPath); !os.IsNotExist(errorValue) {
		t.Fatalf("expected task tmp to be removed, got %v", errorValue)
	}
	if _, errorValue := os.Stat(requesterTemporaryDirectoryPath); errorValue != nil {
		t.Fatalf("expected requester tmp to survive, got %v", errorValue)
	}
}

func TestTaskTemporaryDirectoryCleanerIgnoresUnknownRequester(t *testing.T) {
	cleaner := TaskTemporaryDirectoryCleaner{WorkspaceRootPath: t.TempDir()}
	if errorValue := cleaner.RemoveTaskTemporaryDirectory("", "task-run-1"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := cleaner.RemoveTaskTemporaryDirectory("person-1", ""); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestPOSIXEnvironmentKeepsTaskTemporaryDirectorySeparate(t *testing.T) {
	identity := ExecutionIdentity{UserName: "bc_person_person_1", HomeDirectoryPath: "/workspace/private/people/person-1"}
	taskTemporaryDirectoryPath := TaskTemporaryDirectoryPath(identity.HomeDirectoryPath, "task-run-1")

	environmentVariables := applyPOSIXEnvironment(map[string]string{
		"BLUECLAW_TASK_TMP": taskTemporaryDirectoryPath,
	}, identity)

	if environmentVariables["BLUECLAW_REQUESTER_TMP"] != "/workspace/private/people/person-1/tmp" {
		t.Fatalf("expected person scoped requester tmp, got %+v", environmentVariables)
	}
	if environmentVariables["BLUECLAW_TASK_TMP"] != taskTemporaryDirectoryPath {
		t.Fatalf("expected task scoped tmp to survive, got %+v", environmentVariables)
	}
	if environmentVariables["TMPDIR"] != taskTemporaryDirectoryPath+"/tmp" {
		t.Fatalf("expected scratch inside the task tmp, got %+v", environmentVariables)
	}
	if environmentVariables["XDG_CACHE_HOME"] != "/workspace/private/people/person-1/tmp/.runtime/cache" {
		t.Fatalf("expected person scoped cache to stay outside the task tmp, got %+v", environmentVariables)
	}
}

func TestPOSIXEnvironmentOmitsTaskTemporaryDirectoryWithoutATask(t *testing.T) {
	identity := ExecutionIdentity{UserName: "bc_person_person_1", HomeDirectoryPath: "/workspace/private/people/person-1"}

	environmentVariables := applyPOSIXEnvironment(map[string]string{}, identity)

	if _, isPresent := environmentVariables["BLUECLAW_TASK_TMP"]; isPresent {
		t.Fatalf("expected no task tmp without a task, got %+v", environmentVariables)
	}
	if environmentVariables["TMPDIR"] != "/workspace/private/people/person-1/tmp/.runtime/tmp" {
		t.Fatalf("expected person scoped scratch without a task, got %+v", environmentVariables)
	}
}
