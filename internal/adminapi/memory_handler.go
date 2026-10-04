package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluememo"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

type MemoryHandler struct {
	Stores          *memory.Stores
	IdentityService *identity.IdentityService
}

type memoryFactListResponse struct {
	PersonID string            `json:"personID"`
	Layers   []memoryLayerView `json:"layers"`
	Index    memoryIndexView   `json:"index"`
	Facts    []memoryFactView  `json:"facts"`
}

// memoryLayerView is one layer of the stack a person's memory stands on,
// nearest first, whether or not anything has been written to it yet.
type memoryLayerView struct {
	ScopeType string `json:"scopeType"`
	ScopeID   string `json:"scopeID,omitempty"`
}

type memoryRecallResponse struct {
	Facts          []memoryRecalledView `json:"facts"`
	DegradedReason string               `json:"degradedReason,omitempty"`
}

type memoryRecalledView struct {
	FactID    string `json:"factID"`
	ScopeType string `json:"scopeType"`
	ScopeID   string `json:"scopeID,omitempty"`
	Content   string `json:"content"`
}

const memoryRecallPreviewLimit = 5

type memoryIndexView struct {
	EmbeddingModel string `json:"embeddingModel"`
	Current        int    `json:"current"`
	Stale          int    `json:"stale"`
}

type memoryFactView struct {
	FactID          string    `json:"factID"`
	OriginID        string    `json:"originID"`
	ScopeType       string    `json:"scopeType"`
	ScopeID         string    `json:"scopeID,omitempty"`
	IsStatic        bool      `json:"isStatic"`
	Content         string    `json:"content"`
	OccurredAt      time.Time `json:"occurredAt,omitzero"`
	OccurredUntil   time.Time `json:"occurredUntil,omitzero"`
	ValidUntil      time.Time `json:"validUntil,omitzero"`
	Importance      int       `json:"importance"`
	StorageStrength float64   `json:"storageStrength"`
	CreatedAt       time.Time `json:"createdAt"`
	LastRecalledAt  time.Time `json:"lastRecalledAt,omitzero"`
	ColdSince       time.Time `json:"coldSince,omitzero"`
	TriggerPhrases  []string  `json:"triggerPhrases"`
}

type memoryForgetRequest struct {
	ReaderPersonID string   `json:"readerPersonID"`
	FactIDs        []string `json:"factIDs"`
	Reason         string   `json:"reason"`
}

type memoryForgetResponse struct {
	ForgottenFactIDs []string `json:"forgottenFactIDs"`
}

func (handler MemoryHandler) HandleListFacts(responseWriter http.ResponseWriter, request *http.Request) {
	if !handler.isConfigured() {
		http.Error(responseWriter, "memory store is not configured", http.StatusServiceUnavailable)
		return
	}
	personID := strings.TrimSpace(request.URL.Query().Get("readerPersonID"))
	if personID == "" {
		http.Error(responseWriter, "readerPersonID is required", http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	views, index, errorValue := handler.readableFacts(request.Context(), personID, limit)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, memoryFactListResponse{PersonID: personID, Layers: layerViews(handler.scopes(personID)), Index: index, Facts: views})
}

func layerViews(scopes []memory.Scope) []memoryLayerView {
	views := make([]memoryLayerView, len(scopes))
	for index, scope := range scopes {
		views[index] = memoryLayerView{ScopeType: scope.Kind, ScopeID: scope.ID}
	}
	return views
}

// HandlePreviewRecall answers what would come to mind for a question, read
// through the person's stack the way a task reads it, without reinforcing it.
func (handler MemoryHandler) HandlePreviewRecall(responseWriter http.ResponseWriter, request *http.Request) {
	if !handler.isConfigured() {
		http.Error(responseWriter, "memory store is not configured", http.StatusServiceUnavailable)
		return
	}
	personID := strings.TrimSpace(request.URL.Query().Get("readerPersonID"))
	query := strings.TrimSpace(request.URL.Query().Get("query"))
	if personID == "" || query == "" {
		http.Error(responseWriter, "readerPersonID and query are required", http.StatusBadRequest)
		return
	}
	recalled, errorValue := handler.Stores.PreviewAcross(request.Context(), handler.IdentityService.ResolvePersonAccess(personID), handler.scopes(personID), query, memoryRecallPreviewLimit)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]memoryRecalledView, len(recalled.Facts))
	for index, fact := range recalled.Facts {
		views[index] = memoryRecalledView{FactID: fact.FactID, ScopeType: fact.ScopeType, ScopeID: fact.ScopeID, Content: fact.Content}
	}
	writeJSON(responseWriter, http.StatusOK, memoryRecallResponse{Facts: views, DegradedReason: recalled.DegradedReason})
}

func (handler MemoryHandler) readableFacts(ctx context.Context, personID string, limit int) ([]memoryFactView, memoryIndexView, error) {
	views := []memoryFactView{}
	index := memoryIndexView{}
	held, errorValue := handler.Stores.Held(ctx, handler.scopes(personID))
	if errorValue != nil {
		return nil, index, errorValue
	}
	for _, scope := range held {
		store, errorValue := handler.Stores.Store(ctx, scope)
		if errorValue != nil {
			return nil, index, errorValue
		}
		memories, errorValue := store.Memories(ctx)
		if errorValue != nil {
			return nil, index, errorValue
		}
		indexState, errorValue := store.IndexState(ctx)
		if errorValue != nil {
			return nil, index, errorValue
		}
		index.EmbeddingModel = indexState.EmbeddingModel
		index.Current += indexState.Current
		index.Stale += indexState.Stale
		for _, held := range memories {
			if limit > 0 && len(views) >= limit {
				return views, index, nil
			}
			views = append(views, handler.factView(ctx, store, held, scope))
		}
	}
	return views, index, nil
}

func (handler MemoryHandler) factView(ctx context.Context, store *bluememo.Store, held bluememo.Memory, scope memory.Scope) memoryFactView {
	phrases, errorValue := store.TriggerPhrases(ctx, held.MemoryID)
	if errorValue != nil {
		phrases = nil
	}
	return memoryFactView{
		FactID:          held.MemoryID,
		OriginID:        held.OriginID,
		ScopeType:       scope.Kind,
		ScopeID:         scope.ID,
		IsStatic:        held.IsStatic,
		Content:         held.Content,
		OccurredAt:      held.OccurredAt,
		OccurredUntil:   held.OccurredUntil,
		ValidUntil:      held.ValidUntil,
		Importance:      held.Importance,
		StorageStrength: held.StorageStrength,
		CreatedAt:       held.CreatedAt,
		LastRecalledAt:  held.LastRecalledAt,
		ColdSince:       held.ColdSince,
		TriggerPhrases:  nonNilStrings(phrases),
	}
}

func (handler MemoryHandler) HandleForgetFacts(responseWriter http.ResponseWriter, request *http.Request) {
	if !handler.isConfigured() {
		http.Error(responseWriter, "memory store is not configured", http.StatusServiceUnavailable)
		return
	}
	var forgetRequest memoryForgetRequest
	if errorValue := json.NewDecoder(request.Body).Decode(&forgetRequest); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	personID := strings.TrimSpace(forgetRequest.ReaderPersonID)
	if personID == "" || len(forgetRequest.FactIDs) == 0 {
		http.Error(responseWriter, "readerPersonID and factIDs are required", http.StatusBadRequest)
		return
	}
	forgottenFactIDs, errorValue := handler.Stores.ForgetAcross(request.Context(), handler.scopes(personID), forgetRequest.FactIDs, strings.TrimSpace(forgetRequest.Reason))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	if len(forgottenFactIDs) == 0 {
		http.Error(responseWriter, "none of the facts are live and readable by this person", http.StatusNotFound)
		return
	}
	writeJSON(responseWriter, http.StatusOK, memoryForgetResponse{ForgottenFactIDs: forgottenFactIDs})
}

func (handler MemoryHandler) isConfigured() bool {
	return handler.Stores != nil && handler.IdentityService != nil
}

func (handler MemoryHandler) scopes(personID string) []memory.Scope {
	return memory.ScopesToSearch(handler.IdentityService.ResolvePersonAccess(personID), handler.IdentityService.ContainedCircles())
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
