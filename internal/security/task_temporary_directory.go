package security

import (
	"os"
	"path/filepath"
	"strings"
)

const taskTemporaryDirectoryName = "tasks"

func PersonHomeDirectoryPath(workspaceRootPath string, personID string) string {
	trimmedPersonID := strings.TrimSpace(personID)
	if strings.TrimSpace(workspaceRootPath) == "" || trimmedPersonID == "" {
		return ""
	}
	return filepath.Join(workspaceRootPath, "private", "people", trimmedPersonID)
}

func RequesterTemporaryDirectoryPath(requesterHomePath string) string {
	if strings.TrimSpace(requesterHomePath) == "" {
		return ""
	}
	return filepath.Join(requesterHomePath, "tmp")
}

func TaskTemporaryDirectoryPath(requesterHomePath string, taskRunID string) string {
	requesterTemporaryDirectoryPath := RequesterTemporaryDirectoryPath(requesterHomePath)
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if requesterTemporaryDirectoryPath == "" || trimmedTaskRunID == "" {
		return ""
	}
	return filepath.Join(requesterTemporaryDirectoryPath, taskTemporaryDirectoryName, trimmedTaskRunID)
}

type TaskTemporaryDirectoryCleaner struct {
	WorkspaceRootPath string
}

func (cleaner TaskTemporaryDirectoryCleaner) RemoveTaskTemporaryDirectory(requesterPersonID string, taskRunID string) error {
	requesterHomePath := PersonHomeDirectoryPath(cleaner.WorkspaceRootPath, requesterPersonID)
	taskTemporaryDirectoryPath := TaskTemporaryDirectoryPath(requesterHomePath, taskRunID)
	if taskTemporaryDirectoryPath == "" {
		return nil
	}
	return os.RemoveAll(taskTemporaryDirectoryPath)
}

func ProtectedDirectoryPath(ownerDirectoryPath string) string {
	if strings.TrimSpace(ownerDirectoryPath) == "" {
		return ""
	}
	return filepath.Join(ownerDirectoryPath, ".protected")
}

func PersonMemoryPath(workspaceRootPath string, personID string) string {
	return memoryPathUnder(PersonHomeDirectoryPath(workspaceRootPath, personID))
}

func CircleMemoryPath(workspaceRootPath string, circleID string) string {
	trimmedCircleID := strings.TrimSpace(circleID)
	if strings.TrimSpace(workspaceRootPath) == "" || trimmedCircleID == "" {
		return ""
	}
	return memoryPathUnder(filepath.Join(workspaceRootPath, "circles", trimmedCircleID))
}

func SharedMemoryPath(workspaceRootPath string) string {
	if strings.TrimSpace(workspaceRootPath) == "" {
		return ""
	}
	return memoryPathUnder(filepath.Join(workspaceRootPath, "shared"))
}

func memoryPathUnder(ownerDirectoryPath string) string {
	protectedPath := ProtectedDirectoryPath(ownerDirectoryPath)
	if protectedPath == "" {
		return ""
	}
	return filepath.Join(protectedPath, "memory.db")
}
