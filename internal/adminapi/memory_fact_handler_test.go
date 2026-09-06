package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func TestMemoryFactMutationsRequireAuthorizedReader(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		readerPersonID string
		status         int
		called         bool
	}{
		{name: "owner", readerPersonID: "person-1", status: http.StatusOK, called: true},
		{name: "other person", readerPersonID: "person-2", status: http.StatusForbidden, called: false},
		{name: "missing assertion", status: http.StatusBadRequest, called: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &factEditorStore{}
			service := &memory.MemoryService{}
			service.UseGraphStore(store)
			handler := memoryFactMutationTestHandler(service)
			handler.ReaderPersonID = func(*http.Request) string { return testCase.readerPersonID }
			request := httptest.NewRequest(http.MethodPost, "/admin/api/memory/facts/update", strings.NewReader(`{"namespaceID":"user:person-1","factID":"fact:edge-1","content":"updated"}`))
			responseRecorder := httptest.NewRecorder()
			handler.HandleUpdateFact(responseRecorder, request)
			if responseRecorder.Code != testCase.status {
				t.Fatalf("expected status %d, got %d", testCase.status, responseRecorder.Code)
			}
			if store.updateCalled != testCase.called {
				t.Fatalf("expected update called=%v, got %v", testCase.called, store.updateCalled)
			}
		})
	}
}

func TestMemoryFactDeleteUsesExactAuthorizedTarget(t *testing.T) {
	store := &factEditorStore{}
	service := &memory.MemoryService{}
	service.UseGraphStore(store)
	handler := memoryFactMutationTestHandler(service)
	handler.ReaderPersonID = func(*http.Request) string { return "person-1" }
	request := httptest.NewRequest(http.MethodPost, "/admin/api/memory/facts/delete", strings.NewReader(`{"namespaceID":"user:person-1","factID":"fact:edge-1"}`))
	responseRecorder := httptest.NewRecorder()
	handler.HandleDeleteFact(responseRecorder, request)
	if responseRecorder.Code != http.StatusOK || !store.deleteCalled {
		t.Fatalf("expected authorized delete, status=%d called=%v", responseRecorder.Code, store.deleteCalled)
	}
}

func memoryFactMutationTestHandler(service *memory.MemoryService) MemoryGraphHandler {
	return MemoryGraphHandler{MemoryService: service, Reporter: factMutationReporter{}, Identity: identity.NewIdentityService(policy.PolicyProjection{PersonAccessByPersonID: map[string]policy.PersonAccess{"person-1": {PersonID: "person-1"}, "person-2": {PersonID: "person-2"}}})}
}

type factMutationReporter struct{}

func (factMutationReporter) ListMemoryGraph(context.Context, int) (memory.MemoryGraph, error) {
	return memory.MemoryGraph{}, nil
}
func (factMutationReporter) GetMemoryGraphEpisode(context.Context, string) (memory.MemoryGraphEpisode, bool, error) {
	return memory.MemoryGraphEpisode{}, false, nil
}
func (factMutationReporter) ListMemoryGraphNamespacesByID(context.Context, []string) ([]memory.MemoryGraphNamespace, error) {
	return []memory.MemoryGraphNamespace{{NamespaceID: "user:person-1", ScopeType: memory.ScopeTypeUser, ScopePersonID: "person-1"}}, nil
}

type factEditorStore struct {
	updateCalled bool
	deleteCalled bool
}

func (store *factEditorStore) AddEpisode(context.Context, memory.MemoryEpisode) (memory.MemoryIngestionResult, error) {
	return memory.MemoryIngestionResult{}, nil
}
func (store *factEditorStore) SearchFacts(context.Context, memory.MemorySearchRequest) ([]memory.MemoryFact, error) {
	return nil, nil
}
func (store *factEditorStore) UpdateFact(context.Context, memory.MemoryFactUpdateRequest) (memory.MemoryFact, error) {
	store.updateCalled = true
	return memory.MemoryFact{FactID: "fact:edge-1"}, nil
}
func (store *factEditorStore) DeleteFact(context.Context, memory.MemoryFactDeleteRequest) (memory.MemoryFactMutationResult, error) {
	store.deleteCalled = true
	return memory.MemoryFactMutationResult{FactID: "fact:edge-1", Deleted: true}, nil
}
