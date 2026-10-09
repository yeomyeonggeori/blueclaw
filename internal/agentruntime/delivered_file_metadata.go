package agentruntime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	deliveredFileMetadataSuffix    = ".meta.json"
	deliveredFileMetadataReadLimit = 1 << 16
)

type deliveredFileMetadata struct {
	Holds   json.RawMessage `json:"holds"`
	Notes   []string        `json:"notes"`
	Refusal string          `json:"refusal"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) deliveredMetadata(ctx context.Context, request ToolCatalogRequest, concretePath string) deliveredFileMetadata {
	actor, actorFailure := toolCatalogBuilder.workspaceActorForRequest(ctx, request)
	if actorFailure != nil {
		return deliveredFileMetadata{}
	}
	return readDeliveredMetadata(ctx, actor, concretePath)
}

func readDeliveredMetadata(ctx context.Context, actor security.WorkspaceActor, concretePath string) deliveredFileMetadata {
	metadataPath := concretePath + deliveredFileMetadataSuffix
	if !isAtLeastAsNewAs(ctx, actor, metadataPath, concretePath) {
		return deliveredFileMetadata{}
	}
	content, errorValue := actor.ReadFile(ctx, metadataPath, deliveredFileMetadataReadLimit)
	if errorValue != nil {
		return deliveredFileMetadata{}
	}
	var metadata deliveredFileMetadata
	if json.Unmarshal(content, &metadata) != nil {
		return deliveredFileMetadata{}
	}
	return metadata.bounded()
}

func isAtLeastAsNewAs(ctx context.Context, actor security.WorkspaceActor, path string, otherPath string) bool {
	information, errorValue := actor.Stat(ctx, path)
	if errorValue != nil {
		return false
	}
	other, errorValue := actor.Stat(ctx, otherPath)
	return errorValue == nil && information.ModifiedAtUnix >= other.ModifiedAtUnix
}

func (metadata deliveredFileMetadata) bounded() deliveredFileMetadata {
	holds, isHeld := toolcontract.FileHoldsOf(metadata.Holds)
	if !isHeld {
		holds = nil
	}
	notes := []string{}
	for _, note := range metadata.Notes {
		if trimmed := strings.TrimSpace(note); trimmed != "" {
			notes = append(notes, trimmed)
		}
	}
	return deliveredFileMetadata{Holds: holds, Notes: notes, Refusal: strings.TrimSpace(metadata.Refusal)}
}
