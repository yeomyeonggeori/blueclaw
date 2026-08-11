package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperFailureDetailPreservesExecutionContext(t *testing.T) {
	detail := helperFailureDetail("/usr/local/bin/blueclaw-posix-helper", "capabilities", "permission denied", []byte("stderr tail"))
	for _, expectedFragment := range []string{
		"posix helper capabilities failed",
		"path=/usr/local/bin/blueclaw-posix-helper",
		"output=stderr tail",
		"detail=permission denied",
	} {
		if !strings.Contains(detail, expectedFragment) {
			t.Fatalf("expected helper failure detail to contain %q, got %q", expectedFragment, detail)
		}
	}
}

func TestHelperExecutionFailureDetectsPermissionDeniedBeforeHelperStarts(t *testing.T) {
	if !isHelperExecutionFailure(os.ErrPermission, "") {
		t.Fatal("expected permission denied without stderr to be helper execution failure")
	}
	if isHelperExecutionFailure(os.ErrPermission, "permission denied") {
		t.Fatal("expected helper stderr to be treated as helper-reported failure")
	}
}

func writeFakeCapabilitiesHelper(t *testing.T, helperPath string, script string) {
	t.Helper()
	if errorValue := os.WriteFile(helperPath, []byte(script), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func helperInvocationCount(t *testing.T, invocationLogPath string) int {
	t.Helper()
	content, errorValue := os.ReadFile(invocationLogPath)
	if errorValue != nil {
		if os.IsNotExist(errorValue) {
			return 0
		}
		t.Fatal(errorValue)
	}
	return strings.Count(string(content), "run")
}

func TestEnsureHelperSupportsExecAndFSCachesSuccessfulProbePerHelperPath(t *testing.T) {
	directory := t.TempDir()
	helperPath := filepath.Join(directory, "blueclaw-posix-helper")
	invocationLogPath := filepath.Join(directory, "invocations.log")
	writeFakeCapabilitiesHelper(t, helperPath, "#!/bin/sh\necho run >> "+invocationLogPath+"\nprintf '{\"version\":2,\"capabilities\":[\"exec\",\"fs\"]}'\n")
	for attempt := 0; attempt < 3; attempt++ {
		if errorValue := ensureHelperSupportsExecAndFS(context.Background(), helperPath, "bc_person_test"); errorValue != nil {
			t.Fatalf("expected capabilities probe to succeed, got %v", errorValue)
		}
	}
	if count := helperInvocationCount(t, invocationLogPath); count != 1 {
		t.Fatalf("expected exactly one capabilities probe exec, got %d", count)
	}
}

func TestEnsureHelperSupportsExecAndFSDoesNotCacheFailedProbe(t *testing.T) {
	directory := t.TempDir()
	helperPath := filepath.Join(directory, "blueclaw-posix-helper")
	writeFakeCapabilitiesHelper(t, helperPath, "#!/bin/sh\nexit 1\n")
	if errorValue := ensureHelperSupportsExecAndFS(context.Background(), helperPath, "bc_person_test"); errorValue == nil {
		t.Fatal("expected failing capabilities probe to return an error")
	}
	writeFakeCapabilitiesHelper(t, helperPath, "#!/bin/sh\nprintf '{\"version\":2,\"capabilities\":[\"exec\",\"fs\"]}'\n")
	if errorValue := ensureHelperSupportsExecAndFS(context.Background(), helperPath, "bc_person_test"); errorValue != nil {
		t.Fatalf("expected probe retry after failure to succeed, got %v", errorValue)
	}
}

func TestFSHelperArgumentsCarryTheListOperationAndActorIdentity(t *testing.T) {
	arguments := fsHelperArguments("list_directory", ExecutionIdentity{
		UserName:              "bc_person_abc",
		UserID:                100001,
		GroupID:               100002,
		SupplementaryGroupIDs: []uint32{100003, 100004},
	}, fsRequest{Path: "/workspace/private/people/person-1"})
	expected := []string{
		"fs",
		"--uid", "100001",
		"--gid", "100002",
		"--groups", "100003,100004",
		"--operation", "list_directory",
		"--path", "/workspace/private/people/person-1",
	}
	if strings.Join(arguments, " ") != strings.Join(expected, " ") {
		t.Fatalf("expected the helper to list as the person, got %v", arguments)
	}
}

func TestDirectWorkspaceActorListsDirectoryEntries(t *testing.T) {
	rootPath := t.TempDir()
	if errorValue := os.MkdirAll(filepath.Join(rootPath, "artifacts"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(rootPath, "notes.md"), []byte("hello"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	actor := DirectWorkspaceActor{}
	entries, errorValue := actor.ListDirectory(context.Background(), rootPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	entryByName := map[string]WorkspaceActorDirectoryEntry{}
	for _, entry := range entries {
		entryByName[entry.Name] = entry
	}
	if len(entries) != 2 || !entryByName["artifacts"].IsDirectory || entryByName["notes.md"].SizeBytes != 5 {
		t.Fatalf("expected a directory and a five byte file, got %+v", entries)
	}
	if entryByName["notes.md"].ModifiedAtUnix == 0 {
		t.Fatalf("expected a modification time, got %+v", entryByName["notes.md"])
	}
}

func TestDirectWorkspaceActorReportsPermissionDeniedForAnUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode says")
	}
	unreadablePath := filepath.Join(t.TempDir(), "private-home")
	if errorValue := os.Mkdir(unreadablePath, 0o000); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadablePath, 0o755) })
	_, errorValue := DirectWorkspaceActor{}.ListDirectory(context.Background(), unreadablePath)
	var actorError WorkspaceActorError
	if !errors.As(errorValue, &actorError) || actorError.Code != ActorErrorCodePermissionDenied {
		t.Fatalf("expected a permission denied actor error, got %v", errorValue)
	}
}
