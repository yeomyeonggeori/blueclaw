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
