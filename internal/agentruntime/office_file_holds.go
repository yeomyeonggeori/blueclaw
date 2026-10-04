package agentruntime

import (
	"context"
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (toolCatalogBuilder *ToolCatalogBuilder) deliveredFileHolds(ctx context.Context, request ToolCatalogRequest, concretePath string) json.RawMessage {
	actor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(ctx, request)
	if actorFailure != nil {
		return nil
	}
	content, isRead := readOfficeSnapshotDocument(ctx, actor, concretePath)
	if !isRead {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(content, &fields) != nil {
		return nil
	}
	held := map[string]json.RawMessage{}
	for _, name := range officeContract.SourceContent.Fields {
		if value, isPresent := fields[name]; isPresent {
			held[name] = value
		}
	}
	holds, _ := toolcontract.FileHoldsOf(held)
	return holds
}
