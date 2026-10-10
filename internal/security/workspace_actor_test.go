package security

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
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

func TestAListedDirectoryCarriesHowManyEntriesItHolds(t *testing.T) {
	rootPath := t.TempDir()
	for _, directoryPath := range []string{"full/nested", "empty"} {
		if errorValue := os.MkdirAll(filepath.Join(rootPath, directoryPath), 0o755); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if errorValue := os.WriteFile(filepath.Join(rootPath, "full", "notes.md"), []byte("hello"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	entries, errorValue := ReadWorkspaceDirectory(rootPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	entryCountByName := map[string]*int{}
	for _, entry := range entries {
		entryCountByName[entry.Name] = entry.EntryCount
	}
	if count := entryCountByName["full"]; count == nil || *count != 2 {
		t.Fatalf("expected full to hold a file and a folder, got %+v", entries)
	}
	if count := entryCountByName["empty"]; count == nil || *count != 0 {
		t.Fatalf("expected empty to hold nothing, got %+v", entries)
	}
}

func TestAnUnreadableDirectoryCarriesNoEntryCount(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode says")
	}
	rootPath := t.TempDir()
	unreadablePath := filepath.Join(rootPath, "someone-else")
	if errorValue := os.Mkdir(unreadablePath, 0o000); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadablePath, 0o755) })
	entries, errorValue := ReadWorkspaceDirectory(rootPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(entries) != 1 || entries[0].EntryCount != nil {
		t.Fatalf("expected an unreadable folder to leave its count unknown, got %+v", entries)
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

func fakeStreamingHelperActor(t *testing.T) (POSIXHelperWorkspaceActor, string) {
	t.Helper()
	directory := t.TempDir()
	helperPath := filepath.Join(directory, "helper")
	argumentsPath := filepath.Join(directory, "arguments")
	writeFakeCapabilitiesHelper(t, helperPath, "#!/bin/sh\n"+
		"echo \"$@\" >> "+argumentsPath+"\n"+
		"while [ $# -gt 0 ]; do case \"$1\" in --operation) operation=$2; shift;; --path) target=$2; shift;; esac; shift; done\n"+
		"case \"$operation\" in stream_file) cat \"$target\";; write_file) cat > \"$target\";; *) exit 3;; esac\n")
	return POSIXHelperWorkspaceActor{terminalConfiguration: config.TerminalConfiguration{POSIXHelperPath: helperPath}}, argumentsPath
}

func TestPOSIXHelperActorStreamsAFileThroughTheHelper(t *testing.T) {
	actor, argumentsPath := fakeStreamingHelperActor(t)
	sourcePath := filepath.Join(t.TempDir(), "source.bin")
	content := bytes.Repeat([]byte("stream"), 1<<16)
	if errorValue := os.WriteFile(sourcePath, content, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	var streamed bytes.Buffer
	if errorValue := actor.StreamFile(context.Background(), sourcePath, WorkspaceFileRange{}, &streamed); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !bytes.Equal(streamed.Bytes(), content) {
		t.Fatalf("expected %d bytes, got %d", len(content), streamed.Len())
	}
	if arguments, _ := os.ReadFile(argumentsPath); !strings.Contains(string(arguments), "--operation stream_file") {
		t.Fatalf("expected the stream_file operation, got %q", arguments)
	}
}

func TestPOSIXHelperActorWritesWhatItIsHandedThroughTheHelper(t *testing.T) {
	actor, argumentsPath := fakeStreamingHelperActor(t)
	destinationPath := filepath.Join(t.TempDir(), "destination.bin")
	content := bytes.Repeat([]byte("upload"), 1<<16)
	if errorValue := actor.WriteFileFrom(context.Background(), destinationPath, bytes.NewReader(content)); errorValue != nil {
		t.Fatal(errorValue)
	}
	written, errorValue := os.ReadFile(destinationPath)
	if errorValue != nil || !bytes.Equal(written, content) {
		t.Fatalf("expected %d bytes written, got %d (%v)", len(content), len(written), errorValue)
	}
	if arguments, _ := os.ReadFile(argumentsPath); !strings.Contains(string(arguments), "--operation write_file") {
		t.Fatalf("expected the write_file operation, got %q", arguments)
	}
}
