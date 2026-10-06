package agentruntime

import (
	"context"
	"encoding/json"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type officeSnapshotBlank struct {
	Label string `json:"label"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) deliveredSnapshot(ctx context.Context, request ToolCatalogRequest, concretePath string) map[string]json.RawMessage {
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
	return fields
}

func snapshotHolds(fields map[string]json.RawMessage) json.RawMessage {
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

func snapshotBlankLabels(fields map[string]json.RawMessage) []string {
	var blanks []officeSnapshotBlank
	if json.Unmarshal(fields["blanks"], &blanks) != nil {
		return nil
	}
	labels := []string{}
	for _, blank := range blanks {
		labels = appendUniqueString(labels, blank.Label)
	}
	return labels
}
