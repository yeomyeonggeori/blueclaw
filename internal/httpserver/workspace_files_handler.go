package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/security"
)

// A private home is owned by that person's POSIX user, so only a helper that can
// act for people reads it. Saying the host needs a newer helper is the whole
// diagnosis; the raw "permission denied" this replaces reads as broken ownership.
const tooOldToReadAsThePerson = "this host's workspace helper cannot read a private home; it needs one that can act for a person"

// WorkspaceFilesHandler serves listings, ranged downloads and streamed writes of
// the live workspace filesystem; admind proxies here to show a person their own
// workspace. Every read runs as the person named by the caller, because a private
// home is owned by that person's POSIX user and the blueclaw service cannot read it.
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
	EntryCount  *int   `json:"entryCount,omitempty"`
	ModifiedAt  string `json:"modifiedAt"`
}

func (handler WorkspaceFilesHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.WorkspaceActorFactory == nil || !handler.WorkspaceActorFactory.CanListDirectory(request.Context()) {
		handler.listAsTheService(responseWriter, request)
		return
	}
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
	streamer, isStreamer := actor.(security.WorkspaceFileStreamer)
	if !isStreamer {
		http.Error(responseWriter, "this workspace actor cannot stream a file", http.StatusNotImplemented)
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
	fileRange, status, isSatisfiable := requestedFileRange(request.Header.Get("Range"), stat.SizeBytes)
	responseWriter.Header().Set("Accept-Ranges", "bytes")
	if !isSatisfiable {
		responseWriter.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(stat.SizeBytes, 10))
		http.Error(responseWriter, "that range is past the end of the file", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	fileName := filepath.Base(hostPath)
	responseWriter.Header().Set("Content-Type", "application/octet-stream")
	responseWriter.Header().Set("Content-Length", strconv.FormatInt(fileRange.Length, 10))
	responseWriter.Header().Set("Content-Disposition", "attachment; filename=\""+fileName+"\"")
	responseWriter.Header().Set("Last-Modified", time.Unix(stat.ModifiedAtUnix, 0).UTC().Format(http.TimeFormat))
	if status == http.StatusPartialContent {
		lastByte := fileRange.Offset + fileRange.Length - 1
		responseWriter.Header().Set("Content-Range", "bytes "+strconv.FormatInt(fileRange.Offset, 10)+"-"+strconv.FormatInt(lastByte, 10)+"/"+strconv.FormatInt(stat.SizeBytes, 10))
	}
	responseWriter.WriteHeader(status)
	if fileRange.Length == 0 {
		return
	}
	_ = streamer.StreamFile(request.Context(), hostPath, fileRange, responseWriter)
}

var singleByteRangePattern = regexp.MustCompile(`^bytes=(\d+)-(\d*)$`)

func requestedFileRange(rangeHeader string, sizeBytes int64) (security.WorkspaceFileRange, int, bool) {
	matched := singleByteRangePattern.FindStringSubmatch(strings.TrimSpace(rangeHeader))
	if matched == nil {
		return security.WorkspaceFileRange{Length: sizeBytes}, http.StatusOK, true
	}
	firstByte, _ := strconv.ParseInt(matched[1], 10, 64)
	if firstByte >= sizeBytes {
		return security.WorkspaceFileRange{}, http.StatusRequestedRangeNotSatisfiable, false
	}
	lastByte := sizeBytes - 1
	if matched[2] != "" {
		askedLastByte, _ := strconv.ParseInt(matched[2], 10, 64)
		lastByte = min(askedLastByte, lastByte)
	}
	if lastByte < firstByte {
		return security.WorkspaceFileRange{}, http.StatusRequestedRangeNotSatisfiable, false
	}
	return security.WorkspaceFileRange{Offset: firstByte, Length: lastByte - firstByte + 1}, http.StatusPartialContent, true
}

func (handler WorkspaceFilesHandler) HandleUpload(responseWriter http.ResponseWriter, request *http.Request) {
	actor, hostPath, isResolved := handler.resolveActorAndPath(responseWriter, request)
	if !isResolved {
		return
	}
	streamer, isStreamer := actor.(security.WorkspaceFileStreamer)
	if !isStreamer {
		http.Error(responseWriter, "this workspace actor cannot stream a file", http.StatusNotImplemented)
		return
	}
	if errorValue := actor.MkdirAll(request.Context(), filepath.Dir(hostPath)); errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	counted := &countingReader{source: request.Body}
	if errorValue := streamer.WriteFileFrom(request.Context(), hostPath, counted); errorValue != nil {
		writeWorkspaceActorError(responseWriter, errorValue)
		return
	}
	writeJSON(responseWriter, map[string]any{"name": filepath.Base(hostPath), "sizeBytes": counted.readBytes})
}

type countingReader struct {
	source    io.Reader
	readBytes int64
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	readCount, errorValue := reader.source.Read(buffer)
	reader.readBytes += int64(readCount)
	return readCount, errorValue
}

// A helper too old to list a directory as its owner leaves the service read,
// which is what this endpoint always did. It grants nothing: a private home is
// owned by its person and mode 0700, so the kernel refuses the service there
// exactly as it did before, and starts answering the moment a helper that can
// act for people arrives.
func (handler WorkspaceFilesHandler) listAsTheService(responseWriter http.ResponseWriter, request *http.Request) {
	hostPath, isWithinWorkspace := handler.resolveHostPath(request.URL.Query().Get("path"))
	if !isWithinWorkspace {
		http.Error(responseWriter, "invalid workspace path", http.StatusBadRequest)
		return
	}
	entries, errorValue := security.ReadWorkspaceDirectory(hostPath)
	if errorValue != nil {
		if os.IsNotExist(errorValue) {
			writeJSON(responseWriter, map[string]any{"entries": []workspaceFileEntry{}})
			return
		}
		if os.IsPermission(errorValue) {
			http.Error(responseWriter, tooOldToReadAsThePerson, http.StatusNotImplemented)
			return
		}
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, map[string]any{"entries": visibleWorkspaceEntries(entries)})
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
			EntryCount:  directoryEntry.EntryCount,
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
