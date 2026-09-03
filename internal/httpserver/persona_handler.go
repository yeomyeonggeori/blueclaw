package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const personaDocumentMaximumBytes int64 = 64 * 1024

// PersonaHandler reads and writes one person's own persona document. The
// document lives in that person's private home, which their POSIX user owns and
// the blueclaw service cannot open, so every read and write runs as the person
// the caller names.
type PersonaHandler struct {
	WorkspaceRootPath     string
	WorkspaceActorFactory security.WorkspaceActorFactory
	PersonAccessResolver  PersonAccessResolver
}

func (handler PersonaHandler) HandleReadUser(responseWriter http.ResponseWriter, request *http.Request) {
	actor, documentPath, isResolved := handler.resolveActorAndDocument(responseWriter, request)
	if !isResolved {
		return
	}
	document, errorValue := actor.ReadFile(request.Context(), documentPath, personaDocumentMaximumBytes)
	if security.IsActorNotFoundError(errorValue) {
		writeJSON(responseWriter, persona.NormalizeUser(persona.User{}))
		return
	}
	if errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	user, errorValue := persona.ParseUser(document)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(responseWriter, user)
}

func (handler PersonaHandler) HandleWriteUser(responseWriter http.ResponseWriter, request *http.Request) {
	actor, documentPath, isResolved := handler.resolveActorAndDocument(responseWriter, request)
	if !isResolved {
		return
	}
	body, errorValue := io.ReadAll(io.LimitReader(request.Body, personaDocumentMaximumBytes+1))
	if errorValue != nil || int64(len(body)) > personaDocumentMaximumBytes {
		http.Error(responseWriter, "the document is too large", http.StatusRequestEntityTooLarge)
		return
	}
	user, errorValue := persona.ParseUser(body)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	canonical, errorValue := persona.CanonicalUser(user)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if errorValue := actor.MkdirAll(request.Context(), filepath.Dir(documentPath)); errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	if errorValue := actor.WriteFile(request.Context(), documentPath, canonical); errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	writeJSON(responseWriter, user)
}

func (handler PersonaHandler) resolveActorAndDocument(responseWriter http.ResponseWriter, request *http.Request) (security.WorkspaceActor, string, bool) {
	personID := strings.TrimSpace(request.URL.Query().Get("personID"))
	if personID == "" {
		http.Error(responseWriter, "personID is required", http.StatusBadRequest)
		return nil, "", false
	}
	homePath := security.PersonHomeDirectoryPath(firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath), personID)
	if homePath == "" {
		http.Error(responseWriter, "personID is required", http.StatusBadRequest)
		return nil, "", false
	}
	actor, errorValue := handler.requesterActor(request.Context(), personID)
	if errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return nil, "", false
	}
	return actor, filepath.Join(homePath, persona.UserDocumentRelativePath), true
}

func (handler PersonaHandler) requesterActor(ctx context.Context, personID string) (security.WorkspaceActor, error) {
	if handler.WorkspaceActorFactory == nil || handler.PersonAccessResolver == nil {
		return nil, errors.New("persona documents require a requester actor factory")
	}
	return handler.WorkspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      handler.PersonAccessResolver.ResolvePersonAccess(personID),
		WorkspaceRootPath: firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath),
	})
}
