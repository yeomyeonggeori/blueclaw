package adminapi

import (
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
	Store           *bluememo.Store
	IdentityService *identity.IdentityService
}

type memoryFactListResponse struct {
	PersonID       string           `json:"personID"`
	EmbeddingModel string           `json:"embeddingModel"`
	Profile        bluememo.Profile `json:"profile"`
	Facts          []memoryFactView `json:"facts"`
}

type memoryFactView struct {
	FactID             string    `json:"factID"`
	EpisodeID          string    `json:"episodeID"`
	OwnerPersonID      string    `json:"ownerPersonID"`
	CircleIDs          []string  `json:"circleIDs"`
	Kind               string    `json:"kind"`
	Content            string    `json:"content"`
	ValidFrom          time.Time `json:"validFrom"`
	ValidUntil         time.Time `json:"validUntil,omitzero"`
	ReinforcementCount int       `json:"reinforcementCount"`
	LastRecalledAt     time.Time `json:"lastRecalledAt,omitzero"`
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
	profile, facts, errorValue := handler.Store.ListReadable(request.Context(), handler.reader(personID), limit)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(responseWriter, http.StatusOK, memoryFactListResponse{
		PersonID:       personID,
		EmbeddingModel: handler.Store.EmbeddingModel,
		Profile:        profile,
		Facts:          memoryFactViews(facts),
	})
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
	forgottenFactIDs, errorValue := handler.Store.Forget(request.Context(), handler.reader(personID), forgetRequest.FactIDs, strings.TrimSpace(forgetRequest.Reason))
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
	return handler.Store != nil && handler.Store.Facts != nil && handler.IdentityService != nil
}

func (handler MemoryHandler) reader(personID string) bluememo.Reader {
	return memory.ReaderForAccess(handler.IdentityService.ResolvePersonAccess(personID), handler.IdentityService.ContainedCircles())
}

func memoryFactViews(facts []bluememo.Fact) []memoryFactView {
	views := make([]memoryFactView, 0, len(facts))
	for _, fact := range facts {
		views = append(views, memoryFactView{
			FactID:             fact.FactID,
			EpisodeID:          fact.EpisodeID,
			OwnerPersonID:      fact.OwnerPersonID,
			CircleIDs:          nonNilStrings(fact.CircleIDs),
			Kind:               fact.Kind,
			Content:            fact.Content,
			ValidFrom:          fact.ValidFrom,
			ValidUntil:         fact.ValidUntil,
			ReinforcementCount: fact.ReinforcementCount,
			LastRecalledAt:     fact.LastRecalledAt,
		})
	}
	return views
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
