package agentruntime

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (toolCatalogBuilder *ToolCatalogBuilder) deliveredSource(toolContext context.Context, request ToolCatalogRequest, path string) *toolcontract.DeliveredSource {
	workspaceActor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(toolContext, request)
	if actorFailure != nil {
		return nil
	}
	sourcePath := toolcontract.DeliveredSourcePath(toolCatalogBuilder.nativeRequesterPath(request, path))
	document, errorValue := workspaceActor.ReadFile(toolContext, sourcePath, toolcontract.DeliveredSourceMaximumBytes)
	if errorValue != nil {
		return nil
	}
	source, _ := toolcontract.DeliveredSourceOf(document)
	return source
}
