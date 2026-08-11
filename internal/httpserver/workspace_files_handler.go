package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

const workspaceDownloadMaximumBytes int64 = 64 * 1024 * 1024

// WorkspaceFilesHandler serves read-only listings and downloads of the guest's
// live workspace filesystem. The workspace lives inside the Firecracker guest
// image, so a host-side file browser cannot read it; admind proxies here to show
// a person their own workspace. Every read runs as the person named by the
// caller, because a private home is owned by that person's POSIX user and the
// blueclaw service cannot read it.
type WorkspaceFilesHandler struct {
	WorkspaceRootPath     string
	WorkspaceActorFactory security.WorkspaceActorFactory
	PersonAccessResolver  PersonAccessResolver
}

type PersonAccessResolver interface {
	ResolvePersonAccess(personID string) policy.PersonAccess
}

type workspaceFileEntry struct {
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modifiedAt"`
}

func (handler WorkspaceFilesHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	actor, hostPath, isResolved := handler.resolveActorAndPath(responseWriter, request)
	if !isResolved {
		return
	}
	directoryEntries, errorValue := actor.ListDirectory(request.Context(), hostPath)
	if errorValue != nil {
		if security.IsActorNotFoundError(errorValue) {
			writeJSON(responseWriter, map[string]any{"entries": []workspaceFileEntry{}})
			return
		}
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	writeJSON(responseWriter, map[string]any{"entries": visibleWorkspaceEntries(directoryEntries)})
}

func (handler WorkspaceFilesHandler) HandleDownload(responseWriter http.ResponseWriter, request *http.Request) {
	actor, hostPath, isResolved := handler.resolveActorAndPath(responseWriter, request)
	if !isResolved {
		return
	}
	stat, errorValue := actor.Stat(request.Context(), hostPath)
	if isPermissionDeniedActorError(errorValue) {
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	if errorValue != nil || stat.IsDirectory {
		http.Error(responseWriter, "file not found", http.StatusNotFound)
		return
	}
	if stat.SizeBytes > workspaceDownloadMaximumBytes {
		http.Error(responseWriter, "file is too large to download through the workspace browser", http.StatusRequestEntityTooLarge)
		return
	}
	content, errorValue := actor.ReadFile(request.Context(), hostPath, workspaceDownloadMaximumBytes)
	if errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	fileName := filepath.Base(hostPath)
	responseWriter.Header().Set("Content-Disposition", "attachment; filename=\""+fileName+"\"")
	http.ServeContent(responseWriter, request, fileName, time.Unix(stat.ModifiedAtUnix, 0), bytes.NewReader(content))
}

func (handler WorkspaceFilesHandler) resolveActorAndPath(responseWriter http.ResponseWriter, request *http.Request) (security.WorkspaceActor, string, bool) {
	personID := strings.TrimSpace(request.URL.Query().Get("personID"))
	if personID == "" {
		http.Error(responseWriter, "personID is required", http.StatusBadRequest)
		return nil, "", false
	}
	hostPath, isWithinWorkspace := handler.resolveHostPath(request.URL.Query().Get("path"))
	if !isWithinWorkspace {
		http.Error(responseWriter, "invalid workspace path", http.StatusBadRequest)
		return nil, "", false
	}
	actor, errorValue := handler.requesterActor(request.Context(), personID)
	if errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return nil, "", false
	}
	return actor, hostPath, true
}

func (handler WorkspaceFilesHandler) requesterActor(ctx context.Context, personID string) (security.WorkspaceActor, error) {
	if handler.WorkspaceActorFactory == nil || handler.PersonAccessResolver == nil {
		return nil, errors.New("workspace reads require a requester actor factory")
	}
	return handler.WorkspaceActorFactory.Requester(ctx, security.WorkspaceActorRequest{
		PersonAccess:      handler.PersonAccessResolver.ResolvePersonAccess(personID),
		WorkspaceRootPath: firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath),
	})
}

func visibleWorkspaceEntries(directoryEntries []security.WorkspaceActorDirectoryEntry) []workspaceFileEntry {
	entries := []workspaceFileEntry{}
	for _, directoryEntry := range directoryEntries {
		if directoryEntry.Name == ".blueclaw" {
			continue
		}
		entries = append(entries, workspaceFileEntry{
			Name:        directoryEntry.Name,
			IsDirectory: directoryEntry.IsDirectory,
			Size:        directoryEntry.SizeBytes,
			ModifiedAt:  time.Unix(directoryEntry.ModifiedAtUnix, 0).UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(entries, func(first int, second int) bool {
		if entries[first].IsDirectory != entries[second].IsDirectory {
			return entries[first].IsDirectory
		}
		return strings.ToLower(entries[first].Name) < strings.ToLower(entries[second].Name)
	})
	return entries
}

func isPermissionDeniedActorError(errorValue error) bool {
	var actorError security.WorkspaceActorError
	return errors.As(errorValue, &actorError) && actorError.Code == security.ActorErrorCodePermissionDenied
}

func writeWorkspaceActorError(responseWriter http.ResponseWriter, errorValue error) {
	if isPermissionDeniedActorError(errorValue) {
		http.Error(responseWriter, errorValue.Error(), http.StatusForbidden)
		return
	}
	http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
}

func (handler WorkspaceFilesHandler) resolveHostPath(requestedPath string) (string, bool) {
	rootPath := firstNonEmptyWorkspaceRoot(handler.WorkspaceRootPath)
	cleanRoot := filepath.Clean(rootPath)
	relativePath := strings.TrimPrefix(strings.TrimSpace(requestedPath), "/workspace")
	hostPath := filepath.Clean(filepath.Join(cleanRoot, relativePath))
	if hostPath != cleanRoot && !strings.HasPrefix(hostPath, cleanRoot+string(filepath.Separator)) {
		return "", false
	}
	return hostPath, true
}

func firstNonEmptyWorkspaceRoot(workspaceRootPath string) string {
	if trimmed := strings.TrimSpace(workspaceRootPath); trimmed != "" {
		return trimmed
	}
	return "/workspace"
}

func writeJSON(responseWriter http.ResponseWriter, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(value)
}
