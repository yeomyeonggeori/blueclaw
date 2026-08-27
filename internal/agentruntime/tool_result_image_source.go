package agentruntime

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const mostImageBytesReadBack = 16 << 20

type ToolResultImageSource interface {
	LoadImageContentBase64(ctx context.Context, taskRunID string, devicePath string) (string, error)
}

// A picture a tool read is carried to the model as bytes and is deliberately
// kept out of the ledger, so a turn resumed after an approval or a restart holds
// the attachment without its content. The file is still in the person's own
// workspace, and it is read back the way everything else in there is read: as
// the person who asked.
type RequesterToolResultImageSource struct {
	workspaceActorFactory security.WorkspaceActorFactory
	taskRunStore          taskstate.TaskRunStore
	workspaceRootPath     string
}

func NewRequesterToolResultImageSource(
	workspaceActorFactory security.WorkspaceActorFactory,
	taskRunStore taskstate.TaskRunStore,
	workspaceRootPath string,
) *RequesterToolResultImageSource {
	return &RequesterToolResultImageSource{
		workspaceActorFactory: workspaceActorFactory,
		taskRunStore:          taskRunStore,
		workspaceRootPath:     strings.TrimSpace(workspaceRootPath),
	}
}

func (source *RequesterToolResultImageSource) LoadImageContentBase64(ctx context.Context, taskRunID string, devicePath string) (string, error) {
	requesterActor, errorValue := source.requesterActorFor(ctx, taskRunID)
	if errorValue != nil {
		return "", errorValue
	}
	content, errorValue := requesterActor.ReadFile(ctx, strings.TrimSpace(devicePath), mostImageBytesReadBack)
	if errorValue != nil {
		return "", errorValue
	}
	return base64.StdEncoding.EncodeToString(content), nil
}

func (source *RequesterToolResultImageSource) requesterActorFor(ctx context.Context, taskRunID string) (security.WorkspaceActor, error) {
	if source == nil || source.workspaceActorFactory == nil || source.taskRunStore == nil {
		return nil, errors.New("no workspace identity to read an image as")
	}
	if source.workspaceRootPath == "" {
		return nil, errors.New("an image is read from the workspace it belongs to, and none is configured")
	}
	taskRun, isFound := source.taskRunStore.FindTaskRun(strings.TrimSpace(taskRunID))
	if !isFound || strings.TrimSpace(taskRun.RequesterPersonID) == "" {
		return nil, errors.New("an image is read as the person who asked, and this task names nobody")
	}
	return source.workspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      policy.PersonAccess{PersonID: taskRun.RequesterPersonID},
		WorkspaceRootPath: source.workspaceRootPath,
	})
}
