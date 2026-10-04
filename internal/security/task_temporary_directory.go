package security

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

const taskTemporaryDirectoryName = "tasks"

func PersonHomeDirectoryPath(workspaceRootPath string, personID string) string {
	return subjectDirectoryPath(underWorkspace(workspaceRootPath, "private", "people"), personID)
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

// TaskTemporaryDirectoryCleaner removes a finished task's directory as the
// person it belongs to: it sits in their home, which only they may enter, so
// the service removing it itself is refused by the kernel.
type TaskTemporaryDirectoryCleaner struct {
	WorkspaceRootPath string
	Actors            WorkspaceActorFactory
}

const taskTemporaryDirectoryRemovalTimeoutSecond = 30

func (cleaner TaskTemporaryDirectoryCleaner) RemoveTaskTemporaryDirectory(ctx context.Context, requesterPersonID string, taskRunID string) error {
	requesterHomePath := PersonHomeDirectoryPath(cleaner.WorkspaceRootPath, requesterPersonID)
	taskTemporaryDirectoryPath := TaskTemporaryDirectoryPath(requesterHomePath, taskRunID)
	if taskTemporaryDirectoryPath == "" {
		return nil
	}
	if cleaner.Actors == nil {
		return errors.New("no workspace identity to remove a task directory as")
	}
	actor, errorValue := cleaner.Actors.Requester(ctx, WorkspaceActorRequest{
		PersonAccess:      policy.PersonAccess{PersonID: requesterPersonID},
		WorkspaceRootPath: cleaner.WorkspaceRootPath,
	})
	if errorValue != nil {
		return errorValue
	}
	result, errorValue := actor.Run(ctx, CommandRequest{
		ExecutableName: "rm",
		Arguments:      []string{"-rf", "--", taskTemporaryDirectoryPath},
		TimeoutSecond:  taskTemporaryDirectoryRemovalTimeoutSecond,
	})
	if errorValue != nil {
		return errorValue
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("remove %s as its owner: %s", taskTemporaryDirectoryPath, strings.TrimSpace(result.Stderr))
	}
	return nil
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

// PersonProtectedDirectoryPath is a person's protected directory. It cannot sit
// in their home like a circle's does, because the home is theirs at 0700 and
// the service that writes here could not pass through it.
func PersonProtectedDirectoryPath(workspaceRootPath string, personID string) string {
	return subjectDirectoryPath(PeopleProtectedDirectoryPath(workspaceRootPath), personID)
}

// PeopleProtectedDirectoryPath holds every person's protected directory.
func PeopleProtectedDirectoryPath(workspaceRootPath string) string {
	return underWorkspace(workspaceRootPath, "private", "protected")
}

func CircleDirectoryPath(workspaceRootPath string, circleID string) string {
	return subjectDirectoryPath(CirclesDirectoryPath(workspaceRootPath), circleID)
}

// CirclesDirectoryPath holds every circle's directory.
func CirclesDirectoryPath(workspaceRootPath string) string {
	return underWorkspace(workspaceRootPath, "circles")
}

func underWorkspace(workspaceRootPath string, names ...string) string {
	if strings.TrimSpace(workspaceRootPath) == "" {
		return ""
	}
	return filepath.Join(append([]string{workspaceRootPath}, names...)...)
}

// subjectDirectoryPath answers with nothing for an identifier that cannot name
// a directory, because filepath.Join reads "../.." as a walk upwards and would
// hand back a path outside the workspace for the helper to create and chown.
func subjectDirectoryPath(parentPath string, subjectID string) string {
	trimmedSubjectID := strings.TrimSpace(subjectID)
	if parentPath == "" || !isDirectoryName(trimmedSubjectID) {
		return ""
	}
	return filepath.Join(parentPath, trimmedSubjectID)
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
	return underWorkspace(workspaceRootPath, "shared")
}
