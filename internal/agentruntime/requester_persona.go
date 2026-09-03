package agentruntime

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const (
	requesterPersonaReadTimeout  = 5 * time.Second
	requesterPersonaMaximumBytes = 64 * 1024
)

func requesterPersonaInstruction(factory security.WorkspaceActorFactory, personAccess policy.PersonAccess, workspaceRootPath string) string {
	personID := strings.TrimSpace(personAccess.PersonID)
	if factory == nil || personID == "" {
		return ""
	}
	documentPath := filepath.Join(security.PersonHomeDirectoryPath(workspaceRootPath, personID), persona.UserDocumentRelativePath)
	ctx, cancel := context.WithTimeout(context.Background(), requesterPersonaReadTimeout)
	defer cancel()
	actor, errorValue := factory.Requester(ctx, security.WorkspaceActorRequest{PersonAccess: personAccess, WorkspaceRootPath: workspaceRootPath})
	if errorValue != nil {
		slog.Warn("agentruntime.requester_persona_unreadable", "path", documentPath, "error", errorValue.Error())
		return ""
	}
	document, errorValue := actor.ReadFile(ctx, documentPath, requesterPersonaMaximumBytes)
	if security.IsActorNotFoundError(errorValue) {
		return ""
	}
	if errorValue != nil {
		slog.Warn("agentruntime.requester_persona_unreadable", "path", documentPath, "error", errorValue.Error())
		return ""
	}
	user, document, isRestored, errorValue := persona.ParseWithBackup(persona.ParseUser, document, persona.UserBackupPath(workspaceRootPath, personID))
	if errorValue != nil {
		slog.Warn("agentruntime.requester_persona_rejected", "path", documentPath, "error", errorValue.Error())
		return ""
	}
	if isRestored {
		slog.Warn("agentruntime.requester_persona_restored_from_backup", "path", documentPath)
		if writeError := actor.WriteFile(ctx, documentPath, document); writeError != nil {
			slog.Warn("agentruntime.requester_persona_restore_write_failed", "path", documentPath, "error", writeError.Error())
		}
	}
	return persona.RenderUserInstruction(user)
}
