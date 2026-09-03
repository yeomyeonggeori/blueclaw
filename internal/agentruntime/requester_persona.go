package agentruntime

import (
	"errors"
	"log/slog"
	"os"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
)

func requesterPersonaInstruction(workspaceRootPath string, personID string) string {
	documentPath, isValid := persona.UserDocumentPath(workspaceRootPath, personID)
	if !isValid {
		return ""
	}
	document, errorValue := os.ReadFile(documentPath)
	if errors.Is(errorValue, os.ErrNotExist) {
		return ""
	}
	if errorValue != nil {
		slog.Warn("agentruntime.requester_persona_unreadable", "path", documentPath, "error", errorValue.Error())
		return ""
	}
	user, errorValue := persona.ParseUser(document)
	if errorValue != nil {
		slog.Warn("agentruntime.requester_persona_rejected", "path", documentPath, "error", errorValue.Error())
		return ""
	}
	return persona.RenderUserInstruction(user)
}
