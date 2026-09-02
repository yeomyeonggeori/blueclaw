package adminapi

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluememo"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func memoryHandlerFixture(t *testing.T) (MemoryHandler, *bluememo.InMemoryRepository) {
	t.Helper()
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	repository := bluememo.NewInMemoryRepository()
	episode := bluememo.Episode{EpisodeID: "episode-1", SourceKind: bluememo.EpisodeSourceKindImport, SourceID: "seed", RequesterPersonID: "person-alice", Content: "seed", OccurredAt: now}
	facts := []bluememo.FactWrite{
		{Fact: bluememo.Fact{FactID: "fact-alice", EpisodeID: "episode-1", OwnerPersonID: "person-alice", SubjectPersonID: "person-alice", Kind: bluememo.FactKindPreference, Content: "이샘플 prefers bullet summaries", ValidFrom: now}},
		{Fact: bluememo.Fact{FactID: "fact-bob", EpisodeID: "episode-1", OwnerPersonID: "person-bob", SubjectPersonID: "person-bob", Kind: bluememo.FactKindFact, Content: "박예시 parks on level 3", ValidFrom: now.Add(time.Minute)}},
		{Fact: bluememo.Fact{FactID: "fact-secret", EpisodeID: "episode-1", OwnerPersonID: "person-carol", CircleIDs: []string{"member"}, Kind: bluememo.FactKindFact, Content: "the headcount plan is frozen", SecurityLevelRank: 5, ValidFrom: now}},
		{Fact: bluememo.Fact{FactID: "fact-open", EpisodeID: "episode-1", OwnerPersonID: "person-carol", CircleIDs: []string{"member"}, Kind: bluememo.FactKindFact, Content: "the all-hands is on Thursday", ValidFrom: now.Add(2 * time.Minute)}},
	}
	if errorValue := repository.SaveEpisode(context.Background(), bluememo.EpisodeWrite{Episode: episode, Facts: facts}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := repository.SaveProfile(context.Background(), bluememo.Profile{PersonID: "person-alice", IdentityLines: []string{"이샘플 wants bullets"}, CurrentLines: []string{}, BuiltAt: now}); errorValue != nil {
		t.Fatal(errorValue)
	}
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-alice": {PersonID: "person-alice", Circles: []string{"member"}, SecurityLevelRank: 1},
		},
	})
	store := &bluememo.Store{Facts: repository, Profiles: repository, Jobs: repository, EmbeddingModel: "test-embed", Now: func() time.Time { return now }}
	return MemoryHandler{Store: store, IdentityService: identityService}, repository
}

func TestMemoryHandlerListsWhatThePersonMayReadNewestFirst(t *testing.T) {
	handler, _ := memoryHandlerFixture(t)
	recorder := httptest.NewRecorder()
	handler.HandleListFacts(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/facts?readerPersonID=person-alice", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response memoryFactListResponse
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.PersonID != "person-alice" || response.EmbeddingModel != "test-embed" || len(response.Profile.IdentityLines) != 1 {
		t.Fatalf("expected the person's profile, got %+v", response)
	}
	if len(response.Facts) != 2 || response.Facts[0].FactID != "fact-open" || response.Facts[1].FactID != "fact-alice" {
		t.Fatalf("expected the open workspace fact and the own private fact, newest first, got %+v", response.Facts)
	}
}

func TestMemoryHandlerRequiresAReader(t *testing.T) {
	handler, _ := memoryHandlerFixture(t)
	recorder := httptest.NewRecorder()
	handler.HandleListFacts(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/facts", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without a reader, got %d", recorder.Code)
	}
}

func TestMemoryHandlerForgetsOnlyReadableFacts(t *testing.T) {
	handler, repository := memoryHandlerFixture(t)
	recorder := httptest.NewRecorder()
	handler.HandleForgetFacts(recorder, httptest.NewRequest(http.MethodPost, "/admin/api/memory/facts/forget", strings.NewReader(`{"readerPersonID":"person-alice","factIDs":["fact-alice","fact-bob","fact-secret"],"reason":"asked in the web app"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response memoryForgetResponse
	if errorValue := json.Unmarshal(recorder.Body.Bytes(), &response); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(response.ForgottenFactIDs) != 1 || response.ForgottenFactIDs[0] != "fact-alice" {
		t.Fatalf("expected only the own fact forgotten, got %v", response.ForgottenFactIDs)
	}
	bobFact, _ := repository.FindFact("fact-bob")
	if !bobFact.ForgottenAt.IsZero() {
		t.Fatal("expected another person's fact untouched")
	}
	aliceFact, _ := repository.FindFact("fact-alice")
	if aliceFact.ForgetReason != "asked in the web app" {
		t.Fatalf("expected the reason recorded, got %+v", aliceFact)
	}
	again := httptest.NewRecorder()
	handler.HandleForgetFacts(again, httptest.NewRequest(http.MethodPost, "/admin/api/memory/facts/forget", strings.NewReader(`{"readerPersonID":"person-alice","factIDs":["fact-bob"],"reason":""}`)))
	if again.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when nothing readable was forgotten, got %d", again.Code)
	}
}

func TestMemoryHandlerReportsAMissingStore(t *testing.T) {
	recorder := httptest.NewRecorder()
	MemoryHandler{}.HandleListFacts(recorder, httptest.NewRequest(http.MethodGet, "/admin/api/memory/facts?readerPersonID=person-alice", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without a store, got %d", recorder.Code)
	}
}
