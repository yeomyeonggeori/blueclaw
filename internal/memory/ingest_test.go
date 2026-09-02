package memory_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
)

var ingestNow = time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)

type ingestFixture struct {
	sequence   *int
	repository *memory.InMemoryRepository
	embedder   *memorytest.HashEmbedder
	model      *memorytest.ScriptedModel
	ingester   memory.Ingester
}

func newIngestFixture() ingestFixture {
	repository := memory.NewInMemoryRepository()
	embedder := &memorytest.HashEmbedder{}
	scripted := memorytest.NewScriptedModel()
	store := memory.Store{Facts: repository, Profiles: repository, Jobs: repository, Embedder: embedder, EmbeddingModel: "test-embed", Now: func() time.Time { return ingestNow }}
	sequence := 0
	return ingestFixture{
		sequence:   &sequence,
		repository: repository,
		embedder:   embedder,
		model:      scripted,
		ingester:   memory.Ingester{Store: store, Model: scripted, Now: func() time.Time { return ingestNow }},
	}
}

func (fixture ingestFixture) request(content string) memory.IngestRequest {
	*fixture.sequence++
	sequence := strconv.Itoa(*fixture.sequence)
	return memory.IngestRequest{
		Episode: memory.Episode{
			EpisodeID:         "episode-" + sequence,
			SourceKind:        memory.EpisodeSourceKindExplicit,
			SourceID:          "source-" + sequence,
			RequesterPersonID: "person-alice",
			Content:           content,
			OccurredAt:        ingestNow,
		},
		Reader:        memory.Reader{PersonID: "person-alice", CircleIDs: []string{"circle-platform"}, SecurityLevelRank: 1},
		RequesterName: "이샘플",
		Label:         memory.SecurityLabel{SecurityLevelRank: 1, RequiredClasses: []string{}},
	}
}

func (fixture ingestFixture) ingest(t *testing.T, content string, response map[string]any, mutate func(*memory.IngestRequest)) memory.IngestResult {
	t.Helper()
	fixture.model.Queue(response)
	request := fixture.request(content)
	if mutate != nil {
		mutate(&request)
	}
	result, errorValue := fixture.ingester.Ingest(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("expected ingest to succeed: %v", errorValue)
	}
	return result
}

func TestIngestRecordsNewFactsWithScopeLabelAndSubject(t *testing.T) {
	fixture := newIngestFixture()
	result := fixture.ingest(t, "이샘플 moved to the platform team and prefers bullet summaries", memorytest.IngestResponse(
		memorytest.IngestFact{Content: "이샘플 works in the platform team", Kind: memory.FactKindIdentity, Scope: memory.ScopeTypeWorkspace, SubjectPersonHint: "이샘플", Relation: memory.FactRelationNew},
		memorytest.IngestFact{Content: "이샘플 prefers bullet summaries", Kind: memory.FactKindPreference, Scope: memory.ScopeTypePrivate, SubjectPersonHint: "", Relation: memory.FactRelationNew},
	), nil)
	if len(result.Facts) != 2 || len(result.SupersededFactIDs) != 0 {
		t.Fatalf("expected two new facts, got %+v", result)
	}
	workspaceFact, privateFact := result.Facts[0], result.Facts[1]
	if workspaceFact.ScopeType != memory.ScopeTypeWorkspace || workspaceFact.ScopeID != "" || workspaceFact.SubjectPersonID != "person-alice" || workspaceFact.SecurityLevelRank != 1 {
		t.Fatalf("expected a labelled workspace fact about the requester, got %+v", workspaceFact)
	}
	if privateFact.ScopeType != memory.ScopeTypePrivate || privateFact.ScopeID != "person-alice" || privateFact.SubjectPersonID != "person-alice" || privateFact.SecurityLevelRank != 0 {
		t.Fatalf("expected a private fact keyed by the requester, got %+v", privateFact)
	}
	stored, isFound := fixture.repository.FindFact(privateFact.FactID)
	if !isFound || stored.EmbeddingModel != "test-embed" || stored.EpisodeID != result.EpisodeID {
		t.Fatalf("expected the fact stored with its embedding model and episode, got %+v", stored)
	}
	if job, _, _ := fixture.repository.EnqueueJob(context.Background(), memory.JobKindProfile, "person-alice", ingestNow); job.JobID == "" {
		t.Fatal("expected a profile rebuild to be pending for the subject")
	}
	if !strings.Contains(fixture.model.LastUserMessage(), "Existing facts closest to the source:\n(none)") {
		t.Fatalf("expected the model to see an empty candidate list, got %s", fixture.model.LastUserMessage())
	}
}

func TestIngestSupersedesAndReinforcesOnlyCandidates(t *testing.T) {
	fixture := newIngestFixture()
	first := fixture.ingest(t, "이샘플 works at Google as an engineer and likes terse notes", memorytest.IngestResponse(
		memorytest.IngestFact{Content: "이샘플 works at Google as an engineer", Kind: memory.FactKindFact, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew},
		memorytest.IngestFact{Content: "이샘플 likes terse notes", Kind: memory.FactKindPreference, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew},
	), nil)
	jobFact, preferenceFact := first.Facts[0], first.Facts[1]

	second := fixture.ingest(t, "이샘플 works at Stripe now as a product manager and still likes terse notes", memorytest.IngestResponse(
		memorytest.IngestFact{Content: "이샘플 works at Stripe as a product manager", Kind: memory.FactKindFact, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationSupersedes, RelatedFactID: jobFact.FactID},
		memorytest.IngestFact{Content: "이샘플 likes terse notes", Kind: memory.FactKindPreference, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationReinforces, RelatedFactID: preferenceFact.FactID},
	), nil)
	if len(second.Facts) != 1 || len(second.SupersededFactIDs) != 1 || second.SupersededFactIDs[0] != jobFact.FactID || len(second.ReinforcedFactIDs) != 1 {
		t.Fatalf("expected one supersede and one reinforcement, got %+v", second)
	}
	if !strings.Contains(fixture.model.LastUserMessage(), "id="+jobFact.FactID) {
		t.Fatalf("expected the earlier fact offered as a candidate, got %s", fixture.model.LastUserMessage())
	}
	oldFact, _ := fixture.repository.FindFact(jobFact.FactID)
	if oldFact.SupersededBy != second.Facts[0].FactID {
		t.Fatalf("expected the old fact to point at its replacement, got %+v", oldFact)
	}
	reinforced, _ := fixture.repository.FindFact(preferenceFact.FactID)
	if reinforced.ReinforcementCount != 2 {
		t.Fatalf("expected the preference reinforced to 2, got %d", reinforced.ReinforcementCount)
	}

	fixture.model.Queue(memorytest.IngestResponse(memorytest.IngestFact{Content: "이샘플 works at Acme", Kind: memory.FactKindFact, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationSupersedes, RelatedFactID: "fact-that-was-never-offered"}))
	_, errorValue := fixture.ingester.Ingest(context.Background(), fixture.request("이샘플 works at Acme now"))
	var terminal memory.TerminalJobError
	if !errors.As(errorValue, &terminal) {
		t.Fatalf("expected a terminal error for an invented fact ID, got %v", errorValue)
	}
	if len(fixture.repository.AllFacts()) != 3 {
		t.Fatalf("expected nothing written on a rejected ingest, got %d facts", len(fixture.repository.AllFacts()))
	}
}

func TestIngestNarrowsCircleScopeWithoutAnActiveCircle(t *testing.T) {
	fixture := newIngestFixture()
	response := memorytest.IngestResponse(memorytest.IngestFact{Content: "the platform circle meets on Mondays", Kind: memory.FactKindFact, Scope: memory.ScopeTypeCircle, Relation: memory.FactRelationNew})
	narrowed := fixture.ingest(t, "the platform circle meets on Mondays, said in a direct message", response, nil)
	if narrowed.Facts[0].ScopeType != memory.ScopeTypePrivate || narrowed.Facts[0].ScopeID != "person-alice" {
		t.Fatalf("expected a circle fact without an active circle to narrow to private, got %+v", narrowed.Facts[0])
	}
	kept := fixture.ingest(t, "the platform circle meets on Mondays, said in the circle channel", response, func(request *memory.IngestRequest) {
		request.ActiveCircleID = "circle-platform"
	})
	if kept.Facts[0].ScopeType != memory.ScopeTypeCircle || kept.Facts[0].ScopeID != "circle-platform" {
		t.Fatalf("expected the circle fact kept with an active circle, got %+v", kept.Facts[0])
	}
	foreign := fixture.ingest(t, "the finance circle closes the books on Fridays", memorytest.IngestResponse(memorytest.IngestFact{Content: "the finance circle closes the books on Fridays", Kind: memory.FactKindFact, Scope: memory.ScopeTypeCircle, Relation: memory.FactRelationNew}), func(request *memory.IngestRequest) {
		request.ActiveCircleID = "circle-finance"
	})
	if foreign.Facts[0].ScopeType != memory.ScopeTypePrivate {
		t.Fatalf("expected a circle the requester is not in to narrow to private, got %+v", foreign.Facts[0])
	}
}

func TestIngestTemporaryFactsCarryTheirExpiry(t *testing.T) {
	fixture := newIngestFixture()
	result := fixture.ingest(t, "이샘플 is out of office until 2026-09-05", memorytest.IngestResponse(memorytest.IngestFact{Content: "이샘플 is out of office until 2026-09-05", Kind: memory.FactKindTemporary, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew, ValidUntil: "2026-09-05"}), nil)
	if result.Facts[0].ValidUntil != time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("expected validUntil at the end of the day, got %s", result.Facts[0].ValidUntil)
	}
	for name, fact := range map[string]memorytest.IngestFact{
		"temporary without expiry": {Content: "이샘플 is away", Kind: memory.FactKindTemporary, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew},
		"expiry in the past":       {Content: "이샘플 was away", Kind: memory.FactKindTemporary, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew, ValidUntil: "2026-08-01"},
		"durable fact with expiry": {Content: "이샘플 leads the team", Kind: memory.FactKindFact, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew, ValidUntil: "2026-09-05"},
	} {
		fixture.model.Queue(memorytest.IngestResponse(fact))
		_, errorValue := fixture.ingester.Ingest(context.Background(), fixture.request("이샘플 availability "+name))
		var terminal memory.TerminalJobError
		if !errors.As(errorValue, &terminal) {
			t.Fatalf("%s: expected a terminal error, got %v", name, errorValue)
		}
	}
}

func TestIngestFailuresAreRetryableWhenTheModelOrEmbedderIsDown(t *testing.T) {
	fixture := newIngestFixture()
	fixture.model.Failure = errors.New("gateway timeout")
	_, errorValue := fixture.ingester.Ingest(context.Background(), fixture.request("이샘플 said something worth keeping"))
	var terminal memory.TerminalJobError
	if errorValue == nil || errors.As(errorValue, &terminal) {
		t.Fatalf("expected a retryable model failure, got %v", errorValue)
	}
	fixture.model.Failure = nil
	fixture.embedder.Failure = errors.New("embedding gateway down")
	fixture.model.Queue(memorytest.IngestResponse())
	_, errorValue = fixture.ingester.Ingest(context.Background(), fixture.request("이샘플 said something else worth keeping"))
	if errorValue == nil || errors.As(errorValue, &terminal) {
		t.Fatalf("expected a retryable embedding failure, got %v", errorValue)
	}
	if len(fixture.repository.AllFacts()) != 0 {
		t.Fatal("expected nothing written while dependencies are down")
	}
}

func TestIngestWithNothingWorthRememberingWritesOnlyTheEpisode(t *testing.T) {
	fixture := newIngestFixture()
	result := fixture.ingest(t, "thanks, that is all for today", memorytest.IngestResponse(), nil)
	if len(result.Facts) != 0 || len(fixture.repository.AllFacts()) != 0 {
		t.Fatalf("expected no facts, got %+v", result)
	}
	if _, isCreated, _ := fixture.repository.EnqueueJob(context.Background(), memory.JobKindProfile, "person-alice", ingestNow); !isCreated {
		t.Fatal("expected no profile rebuild to be pending when nothing changed")
	}
}
