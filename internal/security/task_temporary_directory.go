package security

import (
	"os"
	"path/filepath"
	"strings"
)

const taskTemporaryDirectoryName = "tasks"

func PersonHomeDirectoryPath(workspaceRootPath string, personID string) string {
	return subjectDirectoryPath(workspaceRootPath, personID, "private", "people")
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

// ProtectedDirectoryPath is where a subject's own directory keeps what is
// written on their behalf: the service owns it, the subject's group reads it,
// and changes go through the service that owns them.
func ProtectedDirectoryPath(subjectDirectoryPath string) string {
	if strings.TrimSpace(subjectDirectoryPath) == "" {
		return ""
	}
	return filepath.Join(subjectDirectoryPath, ".protected")
}

func CircleDirectoryPath(workspaceRootPath string, circleID string) string {
	return subjectDirectoryPath(workspaceRootPath, circleID, "circles")
}

// subjectDirectoryPath answers with nothing for an identifier that cannot name
// a directory, because filepath.Join reads "../.." as a walk upwards and would
// hand back a path outside the workspace for the helper to create and chown.
func subjectDirectoryPath(workspaceRootPath string, subjectID string, parents ...string) string {
	trimmedSubjectID := strings.TrimSpace(subjectID)
	if strings.TrimSpace(workspaceRootPath) == "" || !isDirectoryName(trimmedSubjectID) {
		return ""
	}
	return filepath.Join(append([]string{workspaceRootPath}, append(parents, trimmedSubjectID)...)...)
}

func isDirectoryName(identifier string) bool {
	if identifier == "" {
		return false
	}
	for _, letter := range identifier {
		isAllowed := (letter >= 'a' && letter <= 'z') ||
			(letter >= 'A' && letter <= 'Z') ||
			(letter >= '0' && letter <= '9') ||
			letter == '-' || letter == '_'
		if !isAllowed {
			return false
		}
	}
	return true
}

func SharedDirectoryPath(workspaceRootPath string) string {
	if strings.TrimSpace(workspaceRootPath) == "" {
		return ""
	}
	return filepath.Join(workspaceRootPath, "shared")
}
