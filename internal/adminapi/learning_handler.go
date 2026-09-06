package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/learning"
)

type SoulLearningReader interface {
	CurrentSoul(context.Context) (learning.SoulRevision, error)
	SoulHistory(context.Context) ([]learning.SoulRevision, error)
}

type LearningHandler struct {
	Store           *learning.Store
	ReaderPersonID  func(*http.Request) string
	ReaderAudience  func(string) string
	IsAdministrator func(string) bool
	Soul            SoulLearningReader
}

func publicSoulRevision(revision learning.SoulRevision) learning.SoulRevision {
	revision.EvidenceIDs = nil
	return revision
}

func (handler LearningHandler) HandleSoul(responseWriter http.ResponseWriter, request *http.Request) {
	_, authorized := handler.access(request, false)
	if !authorized || handler.Soul == nil {
		http.Error(responseWriter, "learning access denied", http.StatusForbidden)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/admin/api/agent-learning/soul")
	if request.Method == http.MethodGet && path == "" {
		revision, errorValue := handler.Soul.CurrentSoul(request.Context())
		if errorValue != nil {
			http.Error(responseWriter, errorValue.Error(), http.StatusNotFound)
			return
		}
		writeJSON(responseWriter, http.StatusOK, publicSoulRevision(revision))
		return
	}
	if request.Method == http.MethodGet && path == "/history" {
		revisions, errorValue := handler.Soul.SoulHistory(request.Context())
		if errorValue != nil {
			http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
			return
		}
		public := make([]learning.SoulRevision, len(revisions))
		for index, revision := range revisions {
			public[index] = publicSoulRevision(revision)
		}
		writeJSON(responseWriter, http.StatusOK, map[string]any{"history": public})
		return
	}
	http.NotFound(responseWriter, request)
}

func (handler LearningHandler) access(request *http.Request, mutation bool) (string, bool) {
	if handler.ReaderPersonID == nil || handler.ReaderAudience == nil {
		return "", false
	}
	reader := strings.TrimSpace(handler.ReaderPersonID(request))
	if reader == "" || strings.TrimSpace(handler.ReaderAudience(reader)) == "" {
		return "", false
	}
	if mutation && (handler.IsAdministrator == nil || !handler.IsAdministrator(reader)) {
		return "", false
	}
	return reader, true
}

func (handler LearningHandler) HandleList(responseWriter http.ResponseWriter, request *http.Request) {
	reader, authorized := handler.access(request, false)
	if !authorized || handler.Store == nil {
		http.Error(responseWriter, "learning access denied", http.StatusForbidden)
		return
	}
	audience := handler.ReaderAudience(reader)
	writeJSON(responseWriter, http.StatusOK, map[string]any{"skills": handler.Store.List(audience, request.URL.Query().Get("includeRetired") == "true"), "settings": handler.Store.Settings(), "activeCount": handler.Store.ActiveCount()})
}

func (handler LearningHandler) HandleGet(responseWriter http.ResponseWriter, request *http.Request) {
	reader, authorized := handler.access(request, false)
	if !authorized || handler.Store == nil {
		http.Error(responseWriter, "learning access denied", http.StatusForbidden)
		return
	}
	id := strings.TrimPrefix(request.URL.Path, "/admin/api/agent-learning/skills/")
	items, errorValue := handler.Store.Get(id, handler.ReaderAudience(reader), request.URL.Query().Get("includeHistory") == "true")
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusNotFound)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"skills": items})
}

func (handler LearningHandler) HandleSettings(responseWriter http.ResponseWriter, request *http.Request) {
	reader, authorized := handler.access(request, request.Method != http.MethodGet)
	if !authorized || handler.Store == nil {
		http.Error(responseWriter, "learning access denied", http.StatusForbidden)
		return
	}
	if request.Method == http.MethodGet {
		writeJSON(responseWriter, http.StatusOK, handler.Store.Settings())
		return
	}
	var settings learning.Settings
	if json.NewDecoder(request.Body).Decode(&settings) != nil {
		http.Error(responseWriter, "invalid settings", http.StatusBadRequest)
		return
	}
	if errorValue := handler.Store.UpdateSettings(settings); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	_ = reader
	writeJSON(responseWriter, http.StatusOK, handler.Store.Settings())
}

func (handler LearningHandler) HandleMutation(responseWriter http.ResponseWriter, request *http.Request) {
	if _, authorized := handler.access(request, true); !authorized || handler.Store == nil {
		http.Error(responseWriter, "learning access denied", http.StatusForbidden)
		return
	}
	var body struct {
		ID        string `json:"id"`
		Protected bool   `json:"protected"`
	}
	if json.NewDecoder(request.Body).Decode(&body) != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(responseWriter, "id is required", http.StatusBadRequest)
		return
	}
	reader := strings.TrimSpace(handler.ReaderPersonID(request))
	if _, errorValue := handler.Store.Get(body.ID, handler.ReaderAudience(reader), false); errorValue != nil {
		http.Error(responseWriter, "learned skill not found", http.StatusNotFound)
		return
	}
	action := strings.TrimPrefix(request.URL.Path, "/admin/api/agent-learning/skills/")
	var errorValue error
	switch action {
	case "retire":
		errorValue = handler.Store.Retire(body.ID)
	case "restore":
		_, errorValue = handler.Store.Restore(body.ID)
	case "protect":
		errorValue = handler.Store.SetProtected(body.ID, body.Protected)
	default:
		http.NotFound(responseWriter, request)
		return
	}
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusConflict)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"id": body.ID, "action": action, "ok": true})
}
