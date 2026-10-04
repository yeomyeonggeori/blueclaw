package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func memoryHandlerFixture(t *testing.T) MemoryHandler {
	t.Helper()
	stores := memorytest.Open(t)
	memorytest.Remember(t, stores, memory.PersonScope("person-alice"), "이샘플 prefers bullet summaries")
	memorytest.Remember(t, stores, memory.PersonScope("person-bob"), "박예시 parks on level 3")
	memorytest.Remember(t, stores, memory.CircleScope("member"), "the all-hands is on Thursday")
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-alice": {PersonID: "person-alice", Circles: []string{"member"}},
		},
	})
	return MemoryHandler{Stores: stores, IdentityService: identityService}
}

func listFacts(t *testing.T, handler MemoryHandler, personID string) memoryFactListResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.HandleListFacts(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/facts?readerPersonID="+personID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response memoryFactListResponse
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatal(errorValue)
	}
	return response
}

func TestMemoryHandlerListsOnlyTheFilesThePersonIsShown(t *testing.T) {
	response := listFacts(t, memoryHandlerFixture(t), "person-alice")
	if response.PersonID != "person-alice" || response.Index.EmbeddingModel != "test-embed" {
		t.Fatalf("expected the person and the index they read, got %+v", response)
	}
	contents := map[string]string{}
	for _, fact := range response.Facts {
		contents[fact.Content] = fact.ScopeType
	}
	if contents["이샘플 prefers bullet summaries"] != memory.ScopePerson {
		t.Fatalf("expected the person's own memory from their own file, got %v", contents)
	}
	if contents["the all-hands is on Thursday"] != memory.ScopeCircle {
		t.Fatalf("expected the circle memory from the circle file, got %v", contents)
	}
	if _, isListed := contents["박예시 parks on level 3"]; isListed {
		t.Fatalf("expected another person's file never opened, got %v", contents)
	}
}

func TestMemoryHandlerRequiresAReader(t *testing.T) {
	recorder := httptest.NewRecorder()
	memoryHandlerFixture(t).HandleListFacts(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/facts", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without a reader, got %d", recorder.Code)
	}
}

func TestMemoryHandlerForgetsOnlyWhatThePersonIsShown(t *testing.T) {
	handler := memoryHandlerFixture(t)
	listed := listFacts(t, handler, "person-alice")
	ownFactID := ""
	for _, fact := range listed.Facts {
		if fact.Content == "이샘플 prefers bullet summaries" {
			ownFactID = fact.FactID
		}
	}
	if ownFactID == "" {
		t.Fatal("expected the person's own memory listed")
	}
	recorder := httptest.NewRecorder()
	body := `{"readerPersonID":"person-alice","factIDs":["` + ownFactID + `"],"reason":"asked in the web app"}`
	handler.HandleForgetFacts(recorder, httptest.NewRequest(http.MethodPost, "/admin/api/memory/facts/forget", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response memoryForgetResponse
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(response.ForgottenFactIDs) != 1 || response.ForgottenFactIDs[0] != ownFactID {
		t.Fatalf("expected the own memory forgotten, got %v", response.ForgottenFactIDs)
	}
	for _, fact := range listFacts(t, handler, "person-alice").Facts {
		if fact.FactID == ownFactID {
			t.Fatal("expected the forgotten memory gone from the listing")
		}
	}
}

func TestMemoryHandlerRefusesAFactInAFileItNeverOpened(t *testing.T) {
	recorder := httptest.NewRecorder()
	memoryHandlerFixture(t).HandleForgetFacts(recorder, httptest.NewRequest(http.MethodPost, "/admin/api/memory/facts/forget", strings.NewReader(`{"readerPersonID":"person-alice","factIDs":["someone-elses-memory"],"reason":""}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when nothing readable was forgotten, got %d", recorder.Code)
	}
}

func TestMemoryHandlerReportsAMissingStore(t *testing.T) {
	recorder := httptest.NewRecorder()
	MemoryHandler{}.HandleListFacts(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/facts?readerPersonID=person-alice", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without a store, got %d", recorder.Code)
	}
}

func TestMemoryHandlerListsTheStackThePersonStandsOnNearestFirst(t *testing.T) {
	handler := memoryHandlerFixture(t)

	response := listFacts(t, handler, "person-alice")

	expected := []memoryLayerView{{ScopeType: memory.ScopePerson, ScopeID: "person-alice"}, {ScopeType: memory.ScopeCircle, ScopeID: "member"}, {ScopeType: memory.ScopeWorkspace}}
	if !slices.Equal(response.Layers, expected) {
		t.Fatalf("expected the stack %v, got %v", expected, response.Layers)
	}
	companyPath, errorValue := handler.Stores.Path(memory.WorkspaceScope())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, statFailure := os.Stat(companyPath); !errors.Is(statFailure, os.ErrNotExist) {
		t.Fatal("listing a person's memory created a file for a layer nobody had written to")
	}
}

func TestMemoryHandlerPreviewsARecallWithoutReinforcingIt(t *testing.T) {
	handler := memoryHandlerFixture(t)
	recorder := httptest.NewRecorder()

	handler.HandlePreviewRecall(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/recall?readerPersonID=person-alice&query=all-hands", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response memoryRecallResponse
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !slices.Contains(response.Facts, memoryRecalledView{FactID: factIDOf(t, handler, "the all-hands is on Thursday"), ScopeType: memory.ScopeCircle, ScopeID: "member", Content: "the all-hands is on Thursday"}) {
		t.Fatalf("expected the circle memory with its circle, got %+v", response.Facts)
	}
	for _, fact := range listFacts(t, handler, "person-alice").Facts {
		if !fact.LastRecalledAt.IsZero() {
			t.Fatalf("a preview reinforced %q", fact.Content)
		}
	}
}

func TestMemoryHandlerPreviewNeedsAQuestion(t *testing.T) {
	recorder := httptest.NewRecorder()
	memoryHandlerFixture(t).HandlePreviewRecall(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/recall?readerPersonID=person-alice", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without a question, got %d", recorder.Code)
	}
}

func factIDOf(t *testing.T, handler MemoryHandler, content string) string {
	t.Helper()
	for _, fact := range listFacts(t, handler, "person-alice").Facts {
		if fact.Content == content {
			return fact.FactID
		}
	}
	t.Fatalf("no fact says %q", content)
	return ""
}
