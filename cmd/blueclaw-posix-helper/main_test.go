package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

func TestRunCapabilitiesReportsFilesystemSupport(t *testing.T) {
	var output bytes.Buffer
	previousOutput := os.Stdout
	readFile, writeFile, errorValue := os.Pipe()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	os.Stdout = writeFile
	errorValue = runCapabilities()
	_ = writeFile.Close()
	os.Stdout = previousOutput
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := output.ReadFrom(readFile); errorValue != nil {
		t.Fatal(errorValue)
	}
	var capabilities helperCapabilitiesDocument
	if errorValue := json.Unmarshal(output.Bytes(), &capabilities); errorValue != nil {
		t.Fatal(errorValue)
	}
	if capabilities.Version != 3 || !containsString(capabilities.Capabilities, "fs") {
		t.Fatalf("expected helper fs capability, got %+v", capabilities)
	}
	if !containsString(capabilities.Capabilities, "reconcile-home") {
		t.Fatalf("expected helper reconcile-home capability, got %+v", capabilities)
	}
	if !containsString(capabilities.Capabilities, "state-sync") {
		t.Fatalf("expected helper state-sync capability, got %+v", capabilities)
	}
}

func TestLoadPOSIXStatePrefersStateDocument(t *testing.T) {
	rootPath := t.TempDir()
	statePath := filepath.Join(rootPath, "state.json")
	stateDocument, errorValue := json.Marshal(security.POSIXState{
		Directories: []security.POSIXDirectory{{
			Path:     "/workspace/circles/member/sites",
			Owner:    "blueclaw",
			Group:    "bc_circle_member",
			ModeText: "2770",
		}},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(statePath, stateDocument, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	state, errorValue := loadPOSIXState(statePath, filepath.Join(rootPath, "missing-policy.json"), "/workspace")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(state.Directories) != 1 || state.Directories[0].Path != "/workspace/circles/member/sites" {
		t.Fatalf("expected state document to be loaded, got %+v", state.Directories)
	}
}

func TestHelperCallerAuthorizationAllowsRootAndBlueclawOnly(t *testing.T) {
	blueclawUserID := 998
	for _, realUserID := range []int{0, blueclawUserID} {
		if !isAuthorizedHelperCaller(realUserID, blueclawUserID) {
			t.Fatalf("expected real uid %d to be authorized", realUserID)
		}
	}
	if isAuthorizedHelperCaller(1001, blueclawUserID) {
		t.Fatal("expected requester uid to be rejected")
	}
}

func TestPrepareExecProcessDropsIdentityBeforeChangingDirectory(t *testing.T) {
	steps := []string{}
	errorValue := prepareExecProcess(
		1001,
		1001,
		[]int{1001, 1002},
		"/workspace/private/people/person-1/sites/site-1/app",
		func(userID uint, groupID uint, groupIDs []int) error {
			steps = append(steps, "identity")
			if userID != 1001 || groupID != 1001 || len(groupIDs) != 2 {
				t.Fatalf("unexpected identity: user=%d group=%d groups=%v", userID, groupID, groupIDs)
			}
			return nil
		},
		func(path string) error {
			steps = append(steps, "chdir")
			if path != "/workspace/private/people/person-1/sites/site-1/app" {
				t.Fatalf("unexpected cwd: %s", path)
			}
			return nil
		},
	)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(steps) != 2 || steps[0] != "identity" || steps[1] != "chdir" {
		t.Fatalf("expected identity before chdir, got %v", steps)
	}
}

func TestCanonicalExecEnvironmentReplacesPATH(t *testing.T) {
	environment := canonicalExecEnvironment([]string{
		"HOME=/workspace/private/people/person-1",
		"PATH=/workspace/private/people/person-1/bin",
		"LANG=C.UTF-8",
	})

	if len(environment) != 3 {
		t.Fatalf("expected canonical environment without duplicate PATH, got %+v", environment)
	}
	if environment[0] != "PATH="+security.CanonicalRuntimePATH {
		t.Fatalf("expected canonical PATH first, got %+v", environment)
	}
	for _, value := range environment[1:] {
		if len(value) >= 5 && value[:5] == "PATH=" {
			t.Fatalf("expected user PATH to be removed, got %+v", environment)
		}
	}
}

func TestPerformFSOperationCopiesFileWithOverwritePolicy(t *testing.T) {
	rootPath := t.TempDir()
	sourcePath := filepath.Join(rootPath, "source.txt")
	destinationPath := filepath.Join(rootPath, "destination.txt")
	if errorValue := os.WriteFile(sourcePath, []byte("first"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := performFSOperation(fsOperationRequest{
		Operation: "copy_file",
		Source:    sourcePath,
		Path:      destinationPath,
		Mode:      0660,
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := performFSOperation(fsOperationRequest{
		Operation: "copy_file",
		Source:    sourcePath,
		Path:      destinationPath,
		Mode:      0660,
	}); errorValue == nil {
		t.Fatal("expected copy_file without overwrite to reject existing destination")
	}
	if errorValue := os.WriteFile(sourcePath, []byte("second"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := performFSOperation(fsOperationRequest{
		Operation: "copy_file",
		Source:    sourcePath,
		Path:      destinationPath,
		Mode:      0660,
		Overwrite: true,
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	document, errorValue := os.ReadFile(destinationPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != "second" {
		t.Fatalf("expected overwritten destination, got %q", string(document))
	}
}

type helperCapabilitiesDocument struct {
	Version      int      `json:"version"`
	Capabilities []string `json:"capabilities"`
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestMkdirAllSkipsExistingDirectoryWithoutChmod(t *testing.T) {
	existingDirectory := t.TempDir()
	if errorValue := os.Chmod(existingDirectory, 0555); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := performFSOperation(fsOperationRequest{Operation: "mkdir_all", Path: existingDirectory, Mode: 02770}); errorValue != nil {
		t.Fatalf("expected existing directory to be left untouched: %v", errorValue)
	}
	fileInformation, errorValue := os.Stat(existingDirectory)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if fileInformation.Mode().Perm() != 0555 {
		t.Fatalf("expected existing directory mode to stay 0555, got %v", fileInformation.Mode().Perm())
	}
}

func TestMkdirAllCreatesAndChmodsOnlyMissingDirectories(t *testing.T) {
	rootDirectory := t.TempDir()
	targetPath := filepath.Join(rootDirectory, "nested", "leaf")
	if errorValue := performFSOperation(fsOperationRequest{Operation: "mkdir_all", Path: targetPath, Mode: 0770}); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, createdPath := range []string{filepath.Join(rootDirectory, "nested"), targetPath} {
		fileInformation, errorValue := os.Stat(createdPath)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if fileInformation.Mode().Perm() != 0770 {
			t.Fatalf("expected created directory %s mode 0770, got %v", createdPath, fileInformation.Mode().Perm())
		}
	}
}

func TestReconcileHomePathScopesToOnePrivateHome(t *testing.T) {
	homePath, errorValue := reconcileHomePath("/workspace", "person-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if homePath != filepath.Join("/workspace", "private", "people", "person-1") {
		t.Fatalf("unexpected home path: %s", homePath)
	}
	for _, personID := range []string{"", "../person-1", "person-1/sites"} {
		if _, errorValue := reconcileHomePath("/workspace", personID); errorValue == nil {
			t.Fatalf("expected person id %q to be rejected", personID)
		}
	}
}

func TestLoadIdentityAllocationTableToleratesEmptyDocument(t *testing.T) {
	workspacePath := t.TempDir()
	if errorValue := os.MkdirAll(filepath.Join(workspacePath, ".blueclaw"), 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, ".blueclaw", "identity-map.json"), []byte("  \n"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	table, errorValue := loadIdentityAllocationTable(workspacePath)
	if errorValue != nil {
		t.Fatalf("empty identity map must regenerate, got: %v", errorValue)
	}
	if table == nil {
		t.Fatal("expected a fresh allocation table")
	}
	for name, identityID := range table.allocations {
		if !strings.HasPrefix(name, "bc_") || identityID < posixIdentityBaseID {
			t.Fatalf("an empty identity map produced the allocation %s=%d, which no projected account on this machine can explain", name, identityID)
		}
	}
}

func TestPerformFSOperationListsDirectoryEntries(t *testing.T) {
	rootPath := t.TempDir()
	if errorValue := os.MkdirAll(filepath.Join(rootPath, "artifacts"), 0755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(rootPath, "notes.md"), []byte("hello"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	response := captureFSOperationResponse(t, fsOperationRequest{Operation: "list_directory", Path: rootPath})
	if len(response.Entries) != 2 {
		t.Fatalf("expected two entries, got %+v", response.Entries)
	}
	entryByName := map[string]security.WorkspaceActorDirectoryEntry{}
	for _, entry := range response.Entries {
		entryByName[entry.Name] = entry
	}
	if !entryByName["artifacts"].IsDirectory || entryByName["notes.md"].SizeBytes != 5 || entryByName["notes.md"].ModifiedAtUnix == 0 {
		t.Fatalf("expected a directory and a five byte file with a modification time, got %+v", response.Entries)
	}
}

func TestPerformFSOperationListDirectoryRequiresAnExistingPath(t *testing.T) {
	if errorValue := performFSOperation(fsOperationRequest{Operation: "list_directory", Path: filepath.Join(t.TempDir(), "absent")}); errorValue == nil {
		t.Fatal("expected list_directory on a missing path to fail")
	}
}

func captureFSOperationResponse(t *testing.T, request fsOperationRequest) fsOperationResponse {
	t.Helper()
	originalStdout := os.Stdout
	readEnd, writeEnd, errorValue := os.Pipe()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	os.Stdout = writeEnd
	operationError := performFSOperation(request)
	os.Stdout = originalStdout
	if errorValue := writeEnd.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
	if operationError != nil {
		t.Fatal(operationError)
	}
	var response fsOperationResponse
	if errorValue := json.NewDecoder(readEnd).Decode(&response); errorValue != nil {
		t.Fatal(errorValue)
	}
	return response
}
