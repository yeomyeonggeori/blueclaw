package agentruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const spillDirectoryName = "spill"

// These mirror the harness port without naming it, so nothing outside the bundled
// harness links the agent loop. bluecollarharness adapts between the two.
type ToolResultSpill struct {
	TaskRunID         string
	ObservationID     string
	ToolName          string
	WorkspaceRootPath string
	SuggestedName     string
	Content           string
}

type ToolResultSpillRef struct {
	Locator       string
	Bytes         int
	RetrievalHint string
}

type ToolResultSpillStore interface {
	SaveToolResultSpill(context.Context, ToolResultSpill) (ToolResultSpillRef, error)
}

// The file is written by the requester's own POSIX identity, so who may read it is decided by
// the same projection that decides every other file in that person's workspace. It lands in the
// task's temporary directory, which the reclaimer removes when the task ends, so a spill lives
// exactly as long as the work that produced it.
type RequesterToolResultSpillStore struct {
	workspaceActorFactory security.WorkspaceActorFactory
	taskRunStore          taskstate.TaskRunStore
}

func NewRequesterToolResultSpillStore(workspaceActorFactory security.WorkspaceActorFactory, taskRunStore taskstate.TaskRunStore) *RequesterToolResultSpillStore {
	return &RequesterToolResultSpillStore{workspaceActorFactory: workspaceActorFactory, taskRunStore: taskRunStore}
}

func (store *RequesterToolResultSpillStore) SaveToolResultSpill(ctx context.Context, spill ToolResultSpill) (ToolResultSpillRef, error) {
	requesterPersonID, spillDirectoryPath, errorValue := store.resolveSpillDestination(spill)
	if errorValue != nil {
		return ToolResultSpillRef{}, errorValue
	}
	requesterActor, errorValue := store.workspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      policy.PersonAccess{PersonID: requesterPersonID},
		WorkspaceRootPath: strings.TrimSpace(spill.WorkspaceRootPath),
	})
	if errorValue != nil {
		return ToolResultSpillRef{}, errorValue
	}
	if errorValue := requesterActor.MkdirAll(ctx, spillDirectoryPath); errorValue != nil {
		return ToolResultSpillRef{}, errorValue
	}
	spillFilePath := filepath.Join(spillDirectoryPath, spillFileName(spill))
	if errorValue := requesterActor.WriteFile(ctx, spillFilePath, []byte(spill.Content)); errorValue != nil {
		return ToolResultSpillRef{}, errorValue
	}
	return ToolResultSpillRef{
		Locator:       spillFilePath,
		Bytes:         len(spill.Content),
		RetrievalHint: "Read it with file_read, or pull out just the part you need with grep or sed through terminal_run.",
	}, nil
}

func (store *RequesterToolResultSpillStore) resolveSpillDestination(spill ToolResultSpill) (string, string, error) {
	if store == nil || store.workspaceActorFactory == nil || store.taskRunStore == nil {
		return "", "", errors.New("no workspace identity to write a spill as")
	}
	workspaceRootPath := strings.TrimSpace(spill.WorkspaceRootPath)
	if workspaceRootPath == "" {
		return "", "", errors.New("a spill needs the workspace it belongs to")
	}
	taskRun, isFound := store.taskRunStore.FindTaskRun(strings.TrimSpace(spill.TaskRunID))
	if !isFound || strings.TrimSpace(taskRun.RequesterPersonID) == "" {
		return "", "", errors.New("a spill is written as the person who asked, and this task names nobody")
	}
	spillDirectoryPath := taskSpillDirectoryPath(workspaceRootPath, taskRun.RequesterPersonID, spill.TaskRunID)
	if spillDirectoryPath == "" {
		return "", "", errors.New("this task has no temporary directory to spill into")
	}
	return taskRun.RequesterPersonID, spillDirectoryPath, nil
}

func taskSpillDirectoryPath(workspaceRootPath string, requesterPersonID string, taskRunID string) string {
	requesterHomePath := security.PersonHomeDirectoryPath(workspaceRootPath, requesterPersonID)
	taskTemporaryDirectoryPath := security.TaskTemporaryDirectoryPath(requesterHomePath, taskRunID)
	if taskTemporaryDirectoryPath == "" {
		return ""
	}
	return filepath.Join(taskTemporaryDirectoryPath, spillDirectoryName)
}

// The observation id goes in front so two results from the same tool in one task never land
// on the same path, and the suggested name is reduced to one path segment.
func spillFileName(spill ToolResultSpill) string {
	suggestedName := filepath.Base(strings.TrimSpace(spill.SuggestedName))
	if suggestedName == "" || suggestedName == "." || suggestedName == string(filepath.Separator) {
		suggestedName = "result.txt"
	}
	observationID := strings.TrimSpace(spill.ObservationID)
	if observationID == "" {
		return suggestedName
	}
	return observationID + "-" + suggestedName
}
