package firecracker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type WorkspaceVolumeService struct {
	MountPath        string
	UnmountPath      string
	SyncPath         string
	TemporaryRootDir string
}

type WorkspaceVolumeMetadata struct {
	HostImagePath     string
	GuestMountPath    string
	DataDirectoryPath string
}

func (workspaceVolumeService WorkspaceVolumeService) RequireWorkspaceImage(workspaceImagePath string) (WorkspaceVolumeMetadata, error) {
	fileInformation, errorValue := os.Stat(workspaceImagePath)
	if errorValue != nil {
		return WorkspaceVolumeMetadata{}, errorValue
	}
	if !fileInformation.Mode().IsRegular() {
		return WorkspaceVolumeMetadata{}, errors.New("workspace image is not a regular file")
	}
	if !workspaceImageIsExt4(workspaceImagePath) {
		return WorkspaceVolumeMetadata{}, errors.New("workspace image exists but is not ext4; refusing to format")
	}
	return workspaceVolumeService.MountWorkspaceMetadata(workspaceImagePath), nil
}

func (workspaceVolumeService WorkspaceVolumeService) MountWorkspaceMetadata(workspaceImagePath string) WorkspaceVolumeMetadata {
	return WorkspaceVolumeMetadata{
		HostImagePath:     workspaceImagePath,
		GuestMountPath:    "/workspace",
		DataDirectoryPath: "/workspace/.blueclaw",
	}
}

func (workspaceVolumeService WorkspaceVolumeService) SyncWorkspaceDirectory(workspaceImagePath string, sourceDirectoryPath string, relativeTargetPath string) error {
	return workspaceVolumeService.syncWorkspaceDirectoryIntoImage(workspaceImagePath, sourceDirectoryPath, relativeTargetPath, false)
}

func (workspaceVolumeService WorkspaceVolumeService) SyncWorkspaceDirectoryPreservingGuestState(workspaceImagePath string, sourceDirectoryPath string) error {
	return workspaceVolumeService.syncWorkspaceDirectoryIntoImage(workspaceImagePath, sourceDirectoryPath, "", true)
}

func (workspaceVolumeService WorkspaceVolumeService) syncWorkspaceDirectoryIntoImage(workspaceImagePath string, sourceDirectoryPath string, relativeTargetPath string, preserveGuestState bool) error {
	if _, errorValue := workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath); errorValue != nil {
		return errorValue
	}
	releaseLock, errorValue := acquireWorkspaceImageLock(workspaceImagePath)
	if errorValue != nil {
		return errorValue
	}
	defer releaseLock()
	if errorValue := ensureWorkspaceImageIsInactive(workspaceImagePath); errorValue != nil {
		return errorValue
	}
	return workspaceVolumeService.writeWorkspaceDirectoryThroughMount(workspaceImagePath, sourceDirectoryPath, relativeTargetPath, preserveGuestState)
}

const workspaceImageLockWaitLimit = 45 * time.Minute
const workspaceImageLockPollInterval = 2 * time.Second

func acquireWorkspaceImageLock(workspaceImagePath string) (func(), error) {
	lockFile, errorValue := os.OpenFile(workspaceImagePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if errorValue != nil {
		return nil, errorValue
	}
	if errorValue := waitForWorkspaceImageLock(lockFile); errorValue != nil {
		_ = lockFile.Close()
		return nil, errorValue
	}
	return func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}, nil
}

func waitForWorkspaceImageLock(lockFile *os.File) error {
	deadline := time.Now().Add(workspaceImageLockWaitLimit)
	hasReportedWait := false
	for {
		if errorValue := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); errorValue == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("workspace image sync was still running after %s", workspaceImageLockWaitLimit)
		}
		if !hasReportedWait {
			fmt.Fprintln(os.Stderr, "waiting for the workspace image sync already in progress")
			hasReportedWait = true
		}
		time.Sleep(workspaceImageLockPollInterval)
	}
}

func ensureWorkspaceImageIsInactive(workspaceImagePath string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	workspaceInformation, errorValue := os.Stat(workspaceImagePath)
	if errorValue != nil {
		return errorValue
	}
	processEntries, errorValue := os.ReadDir("/proc")
	if errorValue != nil {
		return errorValue
	}
	for _, processEntry := range processEntries {
		if _, errorValue := strconv.Atoi(processEntry.Name()); errorValue != nil {
			continue
		}
		fileDescriptorEntries, errorValue := os.ReadDir(filepath.Join("/proc", processEntry.Name(), "fd"))
		if errorValue != nil {
			continue
		}
		for _, fileDescriptorEntry := range fileDescriptorEntries {
			fileDescriptorPath := filepath.Join("/proc", processEntry.Name(), "fd", fileDescriptorEntry.Name())
			fileDescriptorInformation, errorValue := os.Stat(fileDescriptorPath)
			if errorValue == nil && os.SameFile(workspaceInformation, fileDescriptorInformation) {
				return errors.New("workspace image is still open; refusing sync")
			}
		}
	}
	return nil
}

func (workspaceVolumeService WorkspaceVolumeService) writeWorkspaceDirectoryThroughMount(workspaceImagePath string, sourceDirectoryPath string, relativeTargetPath string, preserveGuestConfig bool) (returnError error) {
	if sourceDirectoryPath == "" {
		return nil
	}
	sourceInformation, errorValue := os.Stat(sourceDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	if !sourceInformation.IsDir() {
		return errors.New("workspace source is not a directory")
	}
	if preserveGuestConfig {
		if errorValue := validatePreservedGuestStateSource(sourceDirectoryPath); errorValue != nil {
			return errorValue
		}
	}
	resolvedRelativeTargetPath, errorValue := resolveWorkspaceRelativeTargetPath(relativeTargetPath)
	if errorValue != nil {
		return errorValue
	}

	temporaryRootDirectoryPath := workspaceVolumeService.TemporaryRootDir
	if temporaryRootDirectoryPath == "" {
		temporaryRootDirectoryPath = os.TempDir()
	}
	mountDirectoryPath, errorValue := os.MkdirTemp(temporaryRootDirectoryPath, "blueclaw-workspace-")
	if errorValue != nil {
		return errorValue
	}
	mountPath := workspaceVolumeService.MountPath
	if mountPath == "" {
		mountPath = "mount"
	}
	unmountPath := workspaceVolumeService.UnmountPath
	if unmountPath == "" {
		unmountPath = "umount"
	}
	syncPath := workspaceVolumeService.SyncPath
	if syncPath == "" {
		syncPath = "rsync"
	}
	isWorkspaceMounted := false
	defer func() {
		if isWorkspaceMounted {
			unmountOutput, unmountError := exec.Command(unmountPath, mountDirectoryPath).CombinedOutput()
			if unmountError != nil {
				returnError = errors.Join(returnError, &workspaceUnmountError{output: string(unmountOutput)})
				return
			}
		}
		if errorValue := os.RemoveAll(mountDirectoryPath); errorValue != nil {
			returnError = errors.Join(returnError, errorValue)
		}
	}()

	mountCommand := exec.Command(mountPath, "-o", "loop", workspaceImagePath, mountDirectoryPath)
	if output, errorValue := mountCommand.CombinedOutput(); errorValue != nil {
		return errors.New("mount workspace image: " + string(output))
	}
	isWorkspaceMounted = true
	postgresStateBeforeSync, errorValue := inspectWorkspacePostgresState(mountDirectoryPath)
	if errorValue != nil {
		return errorValue
	}
	if preserveGuestConfig {
		if errorValue := validateProtectedWorkspaceAncestors(mountDirectoryPath); errorValue != nil {
			return errorValue
		}
	}

	targetDirectoryPath, errorValue := ensureWorkspaceTargetDirectory(mountDirectoryPath, resolvedRelativeTargetPath)
	if errorValue != nil {
		return errorValue
	}
	syncCommand := exec.Command(syncPath, workspaceSyncArguments(sourceDirectoryPath, targetDirectoryPath, preserveGuestConfig, resolvedRelativeTargetPath != "")...)
	syncOutput, syncCommandError := syncCommand.CombinedOutput()
	syncError := syncCommandError
	if syncError == nil && preserveGuestConfig && resolvedRelativeTargetPath == "" {
		syncError = seedGuestConfigDirectory(syncPath, sourceDirectoryPath, mountDirectoryPath)
	}
	if syncError == nil && preserveGuestConfig && resolvedRelativeTargetPath != "" {
		syncError = errors.New("guest config preservation requires the workspace root target")
	}
	if syncError == nil {
		postgresStateAfterSync, errorValue := inspectWorkspacePostgresState(mountDirectoryPath)
		if errorValue != nil {
			syncError = errorValue
		} else if postgresStateBeforeSync != postgresStateAfterSync {
			syncError = errors.New("workspace sync changed initialized Postgres state")
		}
	}
	if syncError != nil {
		if syncCommandError != nil {
			return errors.New("sync workspace image: " + string(syncOutput))
		}
		return syncError
	}

	return nil
}

type workspaceUnmountError struct {
	output string
}

func (errorValue *workspaceUnmountError) Error() string {
	return "unmount workspace image: " + errorValue.output
}

func ensureWorkspaceTargetDirectory(workspaceRootPath string, relativeTargetPath string) (string, error) {
	if relativeTargetPath == "" {
		return workspaceRootPath, nil
	}
	currentPath := workspaceRootPath
	for _, pathComponent := range strings.Split(relativeTargetPath, string(filepath.Separator)) {
		currentPath = filepath.Join(currentPath, pathComponent)
		information, errorValue := os.Lstat(currentPath)
		if os.IsNotExist(errorValue) {
			if errorValue := os.Mkdir(currentPath, 0o755); errorValue != nil {
				return "", errorValue
			}
			continue
		}
		if errorValue != nil {
			return "", errorValue
		}
		if !information.IsDir() {
			return "", errors.New("workspace overlay target contains a non-directory path")
		}
	}
	return currentPath, nil
}

type workspacePostgresState struct {
	isInitialized bool
	version       string
	entryCount    int
}

func inspectWorkspacePostgresState(workspaceRootPath string) (workspacePostgresState, error) {
	postgresDataPath := filepath.Join(workspaceRootPath, ".blueclaw", "postgres", "data")
	entries, errorValue := os.ReadDir(postgresDataPath)
	if os.IsNotExist(errorValue) {
		return workspacePostgresState{}, nil
	}
	if errorValue != nil {
		return workspacePostgresState{}, errorValue
	}
	if len(entries) == 0 {
		return workspacePostgresState{}, nil
	}
	versionPath := filepath.Join(postgresDataPath, "PG_VERSION")
	versionInformation, errorValue := os.Lstat(versionPath)
	if errorValue != nil || !versionInformation.Mode().IsRegular() || versionInformation.Size() == 0 {
		return workspacePostgresState{}, errors.New("postgres data is nonempty without a valid PG_VERSION; refusing workspace sync")
	}
	versionDocument, errorValue := os.ReadFile(versionPath)
	if errorValue != nil {
		return workspacePostgresState{}, errorValue
	}
	return workspacePostgresState{isInitialized: true, version: string(versionDocument), entryCount: len(entries)}, nil
}

func validateWorkspacePostgresData(workspaceRootPath string) error {
	_, errorValue := inspectWorkspacePostgresState(workspaceRootPath)
	return errorValue
}

func validatePreservedGuestStateSource(sourceDirectoryPath string) error {
	sourcePaths := []string{
		sourceDirectoryPath,
		filepath.Join(sourceDirectoryPath, ".blueclaw"),
		filepath.Join(sourceDirectoryPath, ".blueclaw", "runtime"),
		filepath.Join(sourceDirectoryPath, ".blueclaw", "config"),
	}
	for _, sourcePath := range sourcePaths {
		information, errorValue := os.Lstat(sourcePath)
		if os.IsNotExist(errorValue) && sourcePath != sourceDirectoryPath {
			continue
		}
		if errorValue != nil {
			return errorValue
		}
		if !information.IsDir() {
			return errors.New("preserved workspace source contains a non-directory control path")
		}
	}
	return nil
}

func validateProtectedWorkspaceAncestors(workspaceRootPath string) error {
	for _, relativePath := range []string{".blueclaw", "circles", "private", "shared"} {
		path := filepath.Join(workspaceRootPath, relativePath)
		information, errorValue := os.Lstat(path)
		if os.IsNotExist(errorValue) {
			continue
		}
		if errorValue != nil {
			return errorValue
		}
		if !information.IsDir() {
			return errors.New("workspace contains a non-directory protected ancestor")
		}
	}
	return nil
}

func resolveWorkspaceRelativeTargetPath(relativeTargetPath string) (string, error) {
	trimmedPath := strings.TrimSpace(relativeTargetPath)
	if trimmedPath == "" {
		return "", nil
	}
	cleanPath := filepath.Clean(trimmedPath)
	if filepath.IsAbs(cleanPath) || cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace relative target path must stay within the workspace")
	}
	normalizedPath := "/" + filepath.ToSlash(cleanPath)
	for _, protectedPath := range protectedGuestStatePaths {
		if normalizedPath == protectedPath || strings.HasPrefix(normalizedPath, protectedPath+"/") {
			return "", errors.New("workspace relative target path overlaps protected guest state")
		}
	}
	return cleanPath, nil
}

func syncWorkspaceImageFile(workspaceImagePath string) error {
	file, errorValue := os.OpenFile(workspaceImagePath, os.O_RDWR, 0)
	if errorValue != nil {
		return errorValue
	}
	syncError := file.Sync()
	closeError := file.Close()
	if syncError != nil {
		return syncError
	}
	return closeError
}

func workspaceSyncArguments(sourceDirectoryPath string, mountDirectoryPath string, preserveGuestConfig bool, deleteExtraneousFiles bool) []string {
	syncArguments := []string{"-a", "--force"}
	if preserveGuestConfig {
		for _, rule := range preservedGuestStateSyncRules {
			syncArguments = append(syncArguments, rule.kind, rule.path)
		}
	}
	if deleteExtraneousFiles {
		syncArguments = append(syncArguments, "--delete")
	}
	return append(syncArguments, filepath.Clean(sourceDirectoryPath)+"/", mountDirectoryPath+"/")
}

type workspaceSyncRule struct {
	kind string
	path string
}

var preservedGuestStateSyncRules = []workspaceSyncRule{
	{kind: "--include", path: "/.blueclaw/"},
	{kind: "--include", path: "/.blueclaw/runtime/***"},
	{kind: "--exclude", path: "*"},
}

var protectedGuestStatePaths = []string{
	"/.blueclaw/blobs",
	"/.blueclaw/config",
	"/.blueclaw/graphiti",
	"/.blueclaw/logs",
	"/.blueclaw/memory",
	"/.blueclaw/migrations",
	"/.blueclaw/postgres",
	"/.blueclaw/tmp",
	"/circles",
	"/private",
	"/shared",
}

func seedGuestConfigDirectory(syncPath string, sourceDirectoryPath string, mountDirectoryPath string) error {
	sourceConfigPath := filepath.Join(filepath.Clean(sourceDirectoryPath), ".blueclaw", "config")
	sourceInformation, errorValue := os.Lstat(sourceConfigPath)
	if os.IsNotExist(errorValue) {
		return nil
	}
	if errorValue != nil {
		return errorValue
	}
	if !sourceInformation.IsDir() {
		return errors.New("source guest config path is not a real directory")
	}
	guestConfigPath := filepath.Join(mountDirectoryPath, ".blueclaw", "config")
	guestInformation, errorValue := os.Lstat(guestConfigPath)
	if os.IsNotExist(errorValue) {
		if errorValue := os.Mkdir(guestConfigPath, 0o750); errorValue != nil {
			return errorValue
		}
	} else if errorValue != nil {
		return errorValue
	} else if !guestInformation.IsDir() {
		return errors.New("guest config path is not a real directory")
	}
	seedCommand := exec.Command(syncPath, "-a", "--ignore-existing", sourceConfigPath+"/", guestConfigPath+"/")
	if output, errorValue := seedCommand.CombinedOutput(); errorValue != nil {
		return errors.New("seed workspace guest config: " + string(output))
	}
	return refreshHostGeneratedGuestConfig(syncPath, sourceConfigPath, guestConfigPath)
}

func refreshHostGeneratedGuestConfig(syncPath string, sourceConfigPath string, guestConfigPath string) error {
	for _, fileName := range hostGeneratedGuestConfigFiles {
		sourceFilePath := filepath.Join(sourceConfigPath, fileName)
		sourceInformation, errorValue := os.Lstat(sourceFilePath)
		if os.IsNotExist(errorValue) {
			continue
		}
		if errorValue != nil {
			return errorValue
		}
		if !sourceInformation.Mode().IsRegular() {
			return errors.New("host-generated guest config source is not a regular file")
		}
		targetFilePath := filepath.Join(guestConfigPath, fileName)
		targetInformation, errorValue := os.Lstat(targetFilePath)
		if errorValue == nil && !targetInformation.Mode().IsRegular() {
			return errors.New("host-generated guest config target is not a regular file")
		}
		if errorValue != nil && !os.IsNotExist(errorValue) {
			return errorValue
		}
		refreshCommand := exec.Command(syncPath, "-a", sourceFilePath, targetFilePath)
		if output, errorValue := refreshCommand.CombinedOutput(); errorValue != nil {
			return errors.New("refresh host-generated guest config: " + string(output))
		}
	}
	return nil
}

var hostGeneratedGuestConfigFiles = []string{"runtime.json"}

func workspaceImageIsExt4(workspaceImagePath string) bool {
	document, errorValue := readWorkspaceImagePrefix(workspaceImagePath, 4096)
	if errorValue != nil || len(document) < 1082 {
		return false
	}
	return document[1080] == 0x53 && document[1081] == 0xef
}

func readWorkspaceImagePrefix(workspaceImagePath string, byteCount int) ([]byte, error) {
	file, errorValue := os.Open(workspaceImagePath)
	if errorValue != nil {
		return nil, errorValue
	}
	defer file.Close()

	document := make([]byte, byteCount)
	readCount, errorValue := io.ReadFull(file, document)
	if errorValue != nil && errorValue != io.EOF && errorValue != io.ErrUnexpectedEOF {
		return nil, errorValue
	}
	return document[:readCount], nil
}
