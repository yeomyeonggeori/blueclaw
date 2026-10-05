package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"path/filepath"

	"github.com/yeomyeonggeori/bluecollar/claimcheck"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const officeDraftClaimsReadLimit = 1 << 20

type officeDraftClaims struct {
	Digest      string             `json:"digest"`
	Unsupported []claimcheck.Claim `json:"unsupported"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) judgeRequestedDraftClaims(ctx context.Context, actor security.WorkspaceActor, request ToolCatalogRequest) {
	if toolCatalogBuilder.claimDecisionModel == nil {
		return
	}
	taskDirectoryPath := security.TaskTemporaryDirectoryPath(toolCatalogBuilder.requesterHomePath(request), toolcontract.TaskRunIDFromContext(ctx))
	contextPath := officeRuntimeContextPath(taskDirectoryPath)
	if contextPath == "" {
		return
	}
	content, errorValue := actor.ReadFile(ctx, filepath.Join(taskDirectoryPath, officeContract.DraftClaims.RequestFile), officeDraftClaimsReadLimit)
	if errorValue != nil {
		return
	}
	digest := sha256Digest(content)
	if toolCatalogBuilder.isDraftJudged(ctx, actor, contextPath, digest) {
		return
	}
	verdict := officeDraftClaims{Digest: digest, Unsupported: toolCatalogBuilder.unsupportedDraftClaims(ctx, request, content)}
	if errorValue := toolCatalogBuilder.writeOfficeRuntimeContext(ctx, actor, request, func(runtimeContext *officeRuntimeContext) { runtimeContext.DraftClaims = &verdict }); errorValue != nil {
		slog.Warn("draft claims were not recorded", "error", errorValue)
	}
}

func (toolCatalogBuilder *ToolCatalogBuilder) unsupportedDraftClaims(ctx context.Context, request ToolCatalogRequest, requestContent []byte) []claimcheck.Claim {
	unsupported := []claimcheck.Claim{}
	var draft struct {
		Claims []claimcheck.Claim `json:"claims"`
	}
	if json.Unmarshal(requestContent, &draft) != nil || len(draft.Claims) == 0 {
		return unsupported
	}
	sources, isEverySourceRead := toolCatalogBuilder.claimSources(ctx, request, officeSnapshot{})
	if !isEverySourceRead {
		return unsupported
	}
	judgment, errorValue := claimcheck.Judge(ctx, toolCatalogBuilder.claimDecisionModel, sources, draft.Claims)
	if errorValue != nil {
		slog.Warn("draft claims were not judged", "error", errorValue)
		return unsupported
	}
	for _, verdict := range judgment.Treated(claimcheck.TreatmentBlank) {
		unsupported = append(unsupported, verdict.Claim)
	}
	return unsupported
}

func (toolCatalogBuilder *ToolCatalogBuilder) isDraftJudged(ctx context.Context, actor security.WorkspaceActor, contextPath string, digest string) bool {
	content, errorValue := actor.ReadFile(ctx, contextPath, officeRuntimeContextReadLimit)
	if errorValue != nil {
		return false
	}
	var recorded officeRuntimeContext
	return json.Unmarshal(content, &recorded) == nil && recorded.DraftClaims != nil && recorded.DraftClaims.Digest == digest
}

func sha256Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
