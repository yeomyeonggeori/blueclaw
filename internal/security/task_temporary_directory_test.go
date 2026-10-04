package security

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
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

	cleaner := TaskTemporaryDirectoryCleaner{WorkspaceRootPath: workspaceRootPath, Actors: commandRunningActors{}}
	if errorValue := cleaner.RemoveTaskTemporaryDirectory(context.Background(), "person-1", "task-run-1"); errorValue != nil {
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
	if errorValue := cleaner.RemoveTaskTemporaryDirectory(context.Background(), "", "task-run-1"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := cleaner.RemoveTaskTemporaryDirectory(context.Background(), "person-1", ""); errorValue != nil {
		t.Fatal(errorValue)
	}
}

// commandRunningActors runs the command a cleaner asks for as this test
// process, so the test exercises the real rm invocation without a helper to
// switch identity.
type commandRunningActors struct {
	WorkspaceActor
}

func (commandRunningActors) Requester(context.Context, WorkspaceActorRequest) (WorkspaceActor, error) {
	return commandRunningActors{}, nil
}

func (commandRunningActors) CanListDirectory(context.Context) bool { return false }

func (commandRunningActors) Run(ctx context.Context, request CommandRequest) (CommandResult, error) {
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, request.ExecutableName, request.Arguments...)
	command.Stderr = &stderr
	var exitError *exec.ExitError
	if errorValue := command.Run(); errors.As(errorValue, &exitError) {
		return CommandResult{ExitCode: exitError.ExitCode(), Stderr: stderr.String()}, nil
	} else if errorValue != nil {
		return CommandResult{}, errorValue
	}
	return CommandResult{}, nil
}

func TestPOSIXEnvironmentKeepsTaskTemporaryDirectorySeparate(t *testing.T) {
	identity := ExecutionIdentity{UserName: "bc_person_person_1", HomeDirectoryPath: "/workspace/private/people/person-1"}
	taskTemporaryDirectoryPath := TaskTemporaryDirectoryPath(identity.HomeDirectoryPath, "task-run-1")

	environmentVariables := applyPOSIXEnvironment(map[string]string{
		"BLUECLAW_TASK_TMP": taskTemporaryDirectoryPath,
	}, identity)

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

func TestASubjectDirectoryAnswersWithNothingForAnIdentifierThatWalksUpwards(t *testing.T) {
	for _, subjectID := range []string{"../../etc", "..", "a/b", "a b", ""} {
		if path := CircleDirectoryPath("/workspace", subjectID); path != "" {
			t.Fatalf("circle %q named the directory %q, which the helper would create and chown", subjectID, path)
		}
		if path := PersonHomeDirectoryPath("/workspace", subjectID); path != "" {
			t.Fatalf("person %q named the directory %q", subjectID, path)
		}
	}
	if path := CircleDirectoryPath("/workspace", "finance"); path != "/workspace/circles/finance" {
		t.Fatalf("an ordinary circle named %q", path)
	}
}
