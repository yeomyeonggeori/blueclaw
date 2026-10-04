package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (toolCatalogBuilder *ToolCatalogBuilder) unsourcedOfficeFileRefusal(ctx context.Context, request ToolCatalogRequest, path string, concretePath string) *toolcontract.ToolResult {
	extension := strings.ToLower(filepath.Ext(concretePath))
	if !slices.Contains(officeContract.DeliverableExtensions, extension) || toolCatalogBuilder.isPersonProvidedFile(request, concretePath) {
		return nil
	}
	taskStartedAt, isTaskKnown := toolCatalogBuilder.taskRunCreatedAt(toolcontract.TaskRunIDFromContext(ctx))
	if !isTaskKnown {
		return nil
	}
	actor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(ctx, request)
	if actorFailure != nil {
		return actorFailure
	}
	information, errorValue := actor.Stat(ctx, concretePath)
	if errorValue != nil || information.ModifiedAtUnix < taskStartedAt.Unix() {
		return nil
	}
	sourcePath := concretePath + officeContract.SourceSuffix
	if source, sourceError := actor.Stat(ctx, sourcePath); sourceError == nil && source.IsRegular {
		return nil
	}
	return unsourcedOfficeFileFailure(path, sourcePath, extension)
}

func (toolCatalogBuilder *ToolCatalogBuilder) isPersonProvidedFile(request ToolCatalogRequest, concretePath string) bool {
	for _, attachment := range toolCatalogBuilder.officeAttachments(request) {
		if filepath.Clean(attachment.Path) == filepath.Clean(concretePath) {
			return true
		}
	}
	return false
}

func unsourcedOfficeFileFailure(path string, sourcePath string, extension string) *toolcontract.ToolResult {
	message := fmt.Sprintf(
		"%s is a new %s file this task wrote without an office command, so it was not delivered: %s, which office merge and office create write beside every file they make, is missing. Make the document with the office skill's merge or create and deliver what it writes; a file the person gave or an earlier task made is delivered from its own path.",
		path, extension, filepath.Base(sourcePath),
	)
	result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "file_deliver", message)
	result.Output.Data = json.RawMessage(MarshalBody(map[string]string{"path": path, "missingSource": sourcePath}))
	return &result
}
