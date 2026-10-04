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
	held := presentFields(fields, officeContract.SourceContent.Fields)
	if len(held) == 0 {
		return nil
	}
	for name, value := range presentFields(fields, officeContract.SourceContent.CompanionFields) {
		held[name] = value
	}
	holds, _ := toolcontract.FileHoldsOf(held)
	return holds
}

func presentFields(fields map[string]json.RawMessage, names []string) map[string]json.RawMessage {
	present := map[string]json.RawMessage{}
	for _, name := range names {
		if value, isPresent := fields[name]; isPresent {
			present[name] = value
		}
	}
	return present
}
