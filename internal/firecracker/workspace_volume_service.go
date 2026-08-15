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
