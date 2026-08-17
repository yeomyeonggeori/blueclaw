package firecracker

import (
	"errors"
	"io"
	"os"
)

type WorkspaceVolumeService struct{}

type WorkspaceVolumeMetadata struct {
	HostImagePath     string
	GuestMountPath    string
	DataDirectoryPath string
}

// A host with no mkfs.ext4 asks the guest to make its own filesystem. The marker is how
// the two tell "never initialised" apart from "initialised and damaged": without it a
// corrupted superblock reads as a fresh disk and is reformatted over the agent's postgres.
const workspaceFormatMarkerSuffix = ".needs-format"

func (workspaceVolumeService WorkspaceVolumeService) EnsureWorkspaceImage(workspaceImagePath string, minimumByteCount int64) (WorkspaceVolumeMetadata, error) {
	if minimumByteCount <= 0 {
		return workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath)
	}
	if _, errorValue := os.Stat(workspaceImagePath); errors.Is(errorValue, os.ErrNotExist) {
		if errorValue := createEmptyWorkspaceImage(workspaceImagePath, minimumByteCount); errorValue != nil {
			return WorkspaceVolumeMetadata{}, errorValue
		}
		return workspaceVolumeService.MountWorkspaceMetadata(workspaceImagePath), nil
	}
	if workspaceImageIsExt4(workspaceImagePath) {
		_ = os.Remove(workspaceFormatMarkerPath(workspaceImagePath))
		return workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath)
	}
	if _, errorValue := os.Stat(workspaceFormatMarkerPath(workspaceImagePath)); errorValue != nil {
		return workspaceVolumeService.RequireWorkspaceImage(workspaceImagePath)
	}
	return workspaceVolumeService.MountWorkspaceMetadata(workspaceImagePath), nil
}

func createEmptyWorkspaceImage(workspaceImagePath string, minimumByteCount int64) error {
	file, errorValue := os.OpenFile(workspaceImagePath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if errorValue != nil {
		return errorValue
	}
	defer file.Close()
	if errorValue := file.Truncate(minimumByteCount); errorValue != nil {
		return errorValue
	}
	return os.WriteFile(workspaceFormatMarkerPath(workspaceImagePath), nil, 0o600)
}

func workspaceFormatMarkerPath(workspaceImagePath string) string {
	return workspaceImagePath + workspaceFormatMarkerSuffix
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
