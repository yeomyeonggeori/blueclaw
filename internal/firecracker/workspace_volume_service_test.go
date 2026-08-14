package firecracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRequireWorkspaceImageReturnsMetadataForExistingExt4(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := writeFakeExt4WorkspaceImage(t, workspacePath)
	workspaceVolumeService := WorkspaceVolumeService{}

	workspaceVolumeMetadata, errorValue := workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath)
	if errorValue != nil {
		t.Fatalf("expected workspace image to be required: %v", errorValue)
	}
	if workspaceVolumeMetadata.GuestMountPath != "/workspace" {
		t.Fatalf("expected guest mount path to match, got %q", workspaceVolumeMetadata.GuestMountPath)
	}
	if workspaceVolumeMetadata.DataDirectoryPath != "/workspace/.blueclaw" {
		t.Fatalf("expected data directory path to match, got %q", workspaceVolumeMetadata.DataDirectoryPath)
	}
	if _, errorValue := workspaceVolumeService.RequireWorkspaceImage(filepath.Join(workspacePath, "missing.ext4")); !os.IsNotExist(errorValue) {
		t.Fatalf("missing workspace image must fail without creation, got %v", errorValue)
	}
}

func TestRequireWorkspaceImageRefusesEveryExistingMalformedFile(t *testing.T) {
	workspacePath := t.TempDir()

	for _, fixture := range []struct {
		name     string
		document []byte
	}{
		{name: "empty"},
		{name: "zero filled", document: make([]byte, 8192)},
		{name: "nonzero", document: []byte("existing workspace state")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			workspaceImagePath := filepath.Join(workspacePath, strings.ReplaceAll(fixture.name, " ", "-")+".ext4")
			if errorValue := os.WriteFile(workspaceImagePath, fixture.document, 0o600); errorValue != nil {
				t.Fatal(errorValue)
			}
			workspaceVolumeService := WorkspaceVolumeService{}
			if _, errorValue := workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath); errorValue == nil || !strings.Contains(errorValue.Error(), "refusing to format") {
				t.Fatalf("expected malformed existing image to fail closed, got %v", errorValue)
			}
			document, errorValue := os.ReadFile(workspaceImagePath)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if !slices.Equal(document, fixture.document) {
				t.Fatal("existing malformed image was modified")
			}
		})
	}
}

func TestAcquireWorkspaceImageLockWaitsForTheSyncInProgress(t *testing.T) {
	workspaceImagePath := filepath.Join(t.TempDir(), "workspace.ext4")
	releaseLock, errorValue := acquireWorkspaceImageLock(workspaceImagePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	secondAcquisition := make(chan error, 1)
	go func() {
		releaseSecondLock, secondError := acquireWorkspaceImageLock(workspaceImagePath)
		if releaseSecondLock != nil {
			releaseSecondLock()
		}
		secondAcquisition <- secondError
	}()

	select {
	case <-secondAcquisition:
		t.Fatal("a second sync took the lock while the first still held it")
	case <-time.After(200 * time.Millisecond):
	}

	releaseLock()
	select {
	case secondError := <-secondAcquisition:
		if secondError != nil {
			t.Fatalf("a waiting sync must proceed once the lock is free, got %v", secondError)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a waiting sync never took the released lock")
	}
}

func TestEnsureWorkspaceImageIsInactiveFindsOpenDescriptor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc descriptor check is Linux-only")
	}
	workspaceImagePath := filepath.Join(t.TempDir(), "workspace.ext4")
	workspaceImage, errorValue := os.Create(workspaceImagePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer workspaceImage.Close()
	if errorValue := ensureWorkspaceImageIsInactive(workspaceImagePath); errorValue == nil {
		t.Fatal("open workspace image descriptor was not detected")
	}
}

func TestSyncWorkspaceDirectoryWritesTheImageItselfAndLeavesNoCopy(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := writeFakeExt4WorkspaceImage(t, workspacePath)
	originalDocument, errorValue := os.ReadFile(workspaceImagePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	sourceDirectoryPath := filepath.Join(workspacePath, "source-skills")
	if errorValue := os.MkdirAll(sourceDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}

	mountPath := writeFakeWorkspaceCommand(t, workspacePath, "mount-image", `
image_path="$3"
mount_path="$4"
ln -s "$image_path" "$mount_path/.image"
mkdir -p "$mount_path/.blueclaw/postgres/data"
printf '16\n' > "$mount_path/.blueclaw/postgres/data/PG_VERSION"
`)
	syncPath := writeFakeWorkspaceCommand(t, workspacePath, "sync-image", `
target_path="${@: -1}"
workspace_root="$target_path"
while [ "$workspace_root" != / ] && [ ! -L "$workspace_root/.image" ]; do
  workspace_root="$(dirname "$workspace_root")"
done
printf overlay >> "$workspace_root/.image"
`)
	unmountPath := writeFakeWorkspaceCommand(t, workspacePath, "unmount-image", "exit 0")
	workspaceVolumeService := WorkspaceVolumeService{
		MountPath:        mountPath,
		SyncPath:         syncPath,
		UnmountPath:      unmountPath,
		TemporaryRootDir: workspacePath,
	}

	if errorValue := workspaceVolumeService.SyncWorkspaceDirectory(workspaceImagePath, sourceDirectoryPath, "skills"); errorValue != nil {
		t.Fatal(errorValue)
	}

	currentDocument, errorValue := os.ReadFile(workspaceImagePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.HasSuffix(string(currentDocument), "overlay") {
		t.Fatal("the sync did not write into the workspace image itself")
	}
	if !strings.HasPrefix(string(currentDocument), string(originalDocument)) {
		t.Fatal("the sync replaced the workspace image instead of writing into it")
	}
	if _, errorValue := os.Stat(workspaceImagePath + ".previous"); !os.IsNotExist(errorValue) {
		t.Fatal("a 64 GB copy is the cost this removes; the sync must not leave one behind")
	}
	entries, errorValue := os.ReadDir(filepath.Dir(workspaceImagePath))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "workspace.ext4.sync-") {
			t.Fatalf("the sync left a copy at %s", entry.Name())
		}
	}
}

func TestSyncWorkspaceDirectoryRefusesMalformedPostgresBeforeSync(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	workspaceDocument := make([]byte, 4096)
	workspaceDocument[1080] = 0x53
	workspaceDocument[1081] = 0xef
	if errorValue := os.WriteFile(workspaceImagePath, workspaceDocument, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	sourceDirectoryPath := filepath.Join(workspacePath, "host-workspace")
	if errorValue := os.MkdirAll(sourceDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	syncMarkerPath := filepath.Join(workspacePath, "sync-called")
	mountPath := writeFakeWorkspaceCommand(t, workspacePath, "mount-malformed-postgres", `
mount_path="$4"
mkdir -p "$mount_path/.blueclaw/postgres/data"
printf history > "$mount_path/.blueclaw/postgres/data/base"
`)
	syncPath := writeFakeWorkspaceCommand(t, workspacePath, "sync-malformed-postgres", "touch "+syncMarkerPath)
	unmountPath := writeFakeWorkspaceCommand(t, workspacePath, "unmount-malformed-postgres", "exit 0")
	workspaceVolumeService := WorkspaceVolumeService{
		MountPath:        mountPath,
		SyncPath:         syncPath,
		UnmountPath:      unmountPath,
		TemporaryRootDir: workspacePath,
	}

	errorValue := workspaceVolumeService.SyncWorkspaceDirectoryPreservingGuestState(workspaceImagePath, sourceDirectoryPath)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "nonempty without a valid PG_VERSION") {
		t.Fatalf("expected malformed postgres data to fail closed, got %v", errorValue)
	}
	if _, errorValue := os.Stat(syncMarkerPath); !os.IsNotExist(errorValue) {
		t.Fatalf("workspace sync ran before postgres validation, got %v", errorValue)
	}
	currentDocument, errorValue := os.ReadFile(workspaceImagePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !slices.Equal(currentDocument, workspaceDocument) {
		t.Fatal("malformed workspace image was modified")
	}
}

func TestSyncWorkspaceDirectoryRetainsCopyWhenUnmountFails(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	workspaceDocument := make([]byte, 4096)
	workspaceDocument[1080] = 0x53
	workspaceDocument[1081] = 0xef
	if errorValue := os.WriteFile(workspaceImagePath, workspaceDocument, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	sourceDirectoryPath := filepath.Join(workspacePath, "skills")
	if errorValue := os.MkdirAll(sourceDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	mountPath := writeFakeWorkspaceCommand(t, workspacePath, "mount-unmount-failure", `
mount_path="$4"
mkdir -p "$mount_path/.blueclaw/postgres/data"
printf '16\n' > "$mount_path/.blueclaw/postgres/data/PG_VERSION"
`)
	syncPath := writeFakeWorkspaceCommand(t, workspacePath, "sync-unmount-failure", "exit 0")
	unmountPath := writeFakeWorkspaceCommand(t, workspacePath, "unmount-failure", "printf busy\nexit 1")
	workspaceVolumeService := WorkspaceVolumeService{
		MountPath:        mountPath,
		SyncPath:         syncPath,
		UnmountPath:      unmountPath,
		TemporaryRootDir: workspacePath,
	}

	errorValue := workspaceVolumeService.SyncWorkspaceDirectory(workspaceImagePath, sourceDirectoryPath, "skills")
	if errorValue == nil || !strings.Contains(errorValue.Error(), "busy") {
		t.Fatalf("an image left mounted is how this repository corrupted a workspace before; the failure must be reported, got %v", errorValue)
	}
}

func TestSyncWorkspaceDirectoryRejectsInitializedPostgresRemoval(t *testing.T) {
	workspacePath := t.TempDir()
	workspaceImagePath := writeFakeExt4WorkspaceImage(t, workspacePath)
	originalDocument, errorValue := os.ReadFile(workspaceImagePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	sourceDirectoryPath := filepath.Join(workspacePath, "host-workspace")
	if errorValue := os.MkdirAll(filepath.Join(sourceDirectoryPath, ".blueclaw"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	mountPath := writeFakeWorkspaceCommand(t, workspacePath, "mount-postgres-removal", `
mount_path="$4"
mkdir -p "$mount_path/.blueclaw/postgres/data"
printf '16\n' > "$mount_path/.blueclaw/postgres/data/PG_VERSION"
printf history > "$mount_path/.blueclaw/postgres/data/base"
`)
	syncPath := writeFakeWorkspaceCommand(t, workspacePath, "sync-postgres-removal", `
target_path="${@: -1}"
rm -rf "$target_path/.blueclaw/postgres/data"
`)
	unmountPath := writeFakeWorkspaceCommand(t, workspacePath, "unmount-postgres-removal", "exit 0")
	workspaceVolumeService := WorkspaceVolumeService{
		MountPath:        mountPath,
		SyncPath:         syncPath,
		UnmountPath:      unmountPath,
		TemporaryRootDir: workspacePath,
	}

	errorValue = workspaceVolumeService.SyncWorkspaceDirectoryPreservingGuestState(workspaceImagePath, sourceDirectoryPath)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "changed initialized Postgres state") {
		t.Fatalf("expected Postgres preservation failure, got %v", errorValue)
	}
	currentDocument, errorValue := os.ReadFile(workspaceImagePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !slices.Equal(currentDocument, originalDocument) {
		t.Fatal("failed Postgres preservation changed the current image")
	}
}

func TestResolveWorkspaceRelativeTargetPathRejectsEscape(t *testing.T) {
	if path, errorValue := resolveWorkspaceRelativeTargetPath("skills"); errorValue != nil || path != "skills" {
		t.Fatalf("expected skills target, got path=%q error=%v", path, errorValue)
	}
	for _, relativeTargetPath := range []string{"/workspace/skills", "../postgres", "skills/../../postgres", "."} {
		if _, errorValue := resolveWorkspaceRelativeTargetPath(relativeTargetPath); errorValue == nil {
			t.Fatalf("expected %q to be rejected", relativeTargetPath)
		}
	}
}

func TestEnsureWorkspaceTargetDirectoryRejectsSymlink(t *testing.T) {
	workspaceRootPath := t.TempDir()
	postgresDataPath := filepath.Join(workspaceRootPath, ".blueclaw", "postgres", "data")
	if errorValue := os.MkdirAll(postgresDataPath, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.Symlink(postgresDataPath, filepath.Join(workspaceRootPath, "skills")); errorValue != nil {
		t.Fatal(errorValue)
	}

	if _, errorValue := ensureWorkspaceTargetDirectory(workspaceRootPath, "skills"); errorValue == nil {
		t.Fatal("workspace overlay must not follow a skills symlink")
	}
}

func TestValidatePreservedGuestStateSourceRejectsBlueclawSymlink(t *testing.T) {
	sourceDirectoryPath := t.TempDir()
	if errorValue := os.Symlink(t.TempDir(), filepath.Join(sourceDirectoryPath, ".blueclaw")); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := validatePreservedGuestStateSource(sourceDirectoryPath); errorValue == nil {
		t.Fatal("preserved guest-state sync must reject a .blueclaw symlink")
	}
}

func TestValidateWorkspacePostgresDataFailsClosedWithoutVersion(t *testing.T) {
	workspaceRootPath := t.TempDir()
	postgresDataPath := filepath.Join(workspaceRootPath, ".blueclaw", "postgres", "data")
	if errorValue := os.MkdirAll(postgresDataPath, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := validateWorkspacePostgresData(workspaceRootPath); errorValue != nil {
		t.Fatalf("empty postgres data should be valid: %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(postgresDataPath, "base"), []byte("existing database"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := validateWorkspacePostgresData(workspaceRootPath); errorValue == nil || !strings.Contains(errorValue.Error(), "refusing workspace sync") {
		t.Fatalf("nonempty data without PG_VERSION must fail closed, got %v", errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(postgresDataPath, "PG_VERSION"), []byte("16\n"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := validateWorkspacePostgresData(workspaceRootPath); errorValue != nil {
		t.Fatalf("postgres data with a valid version should be accepted: %v", errorValue)
	}
}

func TestWorkspaceSyncArgumentsForceSymlinkReplacement(t *testing.T) {
	arguments := workspaceSyncArguments("/source/workspace", "/mounted/workspace", false, false)

	if !slices.Contains(arguments, "--force") {
		t.Fatalf("expected rsync arguments to force symlink replacement, got %+v", arguments)
	}
	if arguments[len(arguments)-2] != "/source/workspace/" {
		t.Fatalf("expected source directory to include trailing slash, got %+v", arguments)
	}
	if arguments[len(arguments)-1] != "/mounted/workspace/" {
		t.Fatalf("expected mount directory to include trailing slash, got %+v", arguments)
	}
}

func TestWorkspaceSyncArgumentsPreserveGuestConfig(t *testing.T) {
	arguments := workspaceSyncArguments("/source/workspace", "/mounted/workspace", true, false)

	for _, expectedArgument := range []string{"/.blueclaw/", "/.blueclaw/runtime/***", "*"} {
		if !slices.Contains(arguments, expectedArgument) {
			t.Fatalf("expected control-state rule %q in %+v", expectedArgument, arguments)
		}
	}
	for _, forbiddenArgument := range []string{"/private", "/circles", "/shared", "/.blueclaw/postgres"} {
		if slices.Contains(arguments, forbiddenArgument) {
			t.Fatalf("payload sync should not address content or database path %q in %+v", forbiddenArgument, arguments)
		}
	}
}

func TestWorkspaceSyncArgumentsDeleteOnlyInsideAnOverlayTarget(t *testing.T) {
	arguments := workspaceSyncArguments("/source/skills", "/mounted/workspace/skills", false, true)

	if !slices.Contains(arguments, "--delete") {
		t.Fatalf("expected exact skills overlay, got %+v", arguments)
	}
}

func writeFakeExt4WorkspaceImage(t *testing.T, workspacePath string) string {
	t.Helper()
	workspaceImagePath := filepath.Join(workspacePath, "workspace.ext4")
	document := make([]byte, 4096)
	document[1080] = 0x53
	document[1081] = 0xef
	if errorValue := os.WriteFile(workspaceImagePath, document, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	return workspaceImagePath
}

func writeFakeWorkspaceCommand(t *testing.T, workspacePath string, commandName string, commandBody string) string {
	t.Helper()
	commandPath := filepath.Join(workspacePath, commandName)
	commandDocument := "#!/usr/bin/env bash\nset -euo pipefail\n" + commandBody + "\n"
	if errorValue := os.WriteFile(commandPath, []byte(commandDocument), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	return commandPath
}

func TestSeedGuestConfigRefreshesRuntimeButPreservesPolicy(t *testing.T) {
	if _, errorValue := exec.LookPath("rsync"); errorValue != nil {
		t.Skip("rsync not available")
	}
	hostWorkspace := t.TempDir()
	hostConfig := filepath.Join(hostWorkspace, ".blueclaw", "config")
	if errorValue := os.MkdirAll(hostConfig, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeConfigFile(t, hostConfig, "runtime.json", `{"op":"task_add"}`)
	writeConfigFile(t, hostConfig, "policy.json", `{"people":["host-stale"]}`)

	guestMount := t.TempDir()
	guestConfig := filepath.Join(guestMount, ".blueclaw", "config")
	if errorValue := os.MkdirAll(guestConfig, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeConfigFile(t, guestConfig, "runtime.json", `{"op":"flow.task.add"}`)
	writeConfigFile(t, guestConfig, "policy.json", `{"people":["guest-live"]}`)

	if errorValue := seedGuestConfigDirectory("rsync", hostWorkspace, guestMount); errorValue != nil {
		t.Fatal(errorValue)
	}

	if got := readConfigFile(t, guestConfig, "runtime.json"); got != `{"op":"task_add"}` {
		t.Fatalf("runtime.json should be refreshed from host, got %q", got)
	}
	if got := readConfigFile(t, guestConfig, "policy.json"); got != `{"people":["guest-live"]}` {
		t.Fatalf("policy.json should be preserved (runtime-managed), got %q", got)
	}
}

func TestSeedGuestConfigRejectsSymlinkTarget(t *testing.T) {
	sourceWorkspace := t.TempDir()
	sourceConfigPath := filepath.Join(sourceWorkspace, ".blueclaw", "config")
	if errorValue := os.MkdirAll(sourceConfigPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeConfigFile(t, sourceConfigPath, "runtime.json", `{}`)
	guestWorkspace := t.TempDir()
	guestBlueclawPath := filepath.Join(guestWorkspace, ".blueclaw")
	if errorValue := os.MkdirAll(guestBlueclawPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.Symlink(t.TempDir(), filepath.Join(guestBlueclawPath, "config")); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := seedGuestConfigDirectory("rsync", sourceWorkspace, guestWorkspace); errorValue == nil {
		t.Fatal("guest config symlink was accepted")
	}
}

func writeConfigFile(t *testing.T, directory string, name string, content string) {
	t.Helper()
	if errorValue := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func readConfigFile(t *testing.T, directory string, name string) string {
	t.Helper()
	content, errorValue := os.ReadFile(filepath.Join(directory, name))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(content)
}
