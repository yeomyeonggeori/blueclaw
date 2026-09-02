package integration

import (
	"context"
	"github.com/yeomyeonggeori/bluememo"
	bluememopostgres "github.com/yeomyeonggeori/bluememo/postgres"
	"os"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"

	"github.com/yeomyeonggeori/blueclaw/internal/store/postgres"
)

const memoryTestPersonPrefix = "memtest-"

type memoryStoreFixture struct {
	database  postgres.Database
	facts     bluememopostgres.FactRepository
	jobs      bluememopostgres.JobRepository
	now       time.Time
	hasVector bool
}

func openMemoryStoreFixture(t *testing.T) memoryStoreFixture {
	t.Helper()
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("set BLUECLAW_TEST_POSTGRES_URL to run the memory store checks")
	}
	ctx := context.Background()
	database, errorValue := postgres.OpenDatabase(ctx, connectionString)
	if errorValue != nil {
		t.Fatalf("expected the test database to open: %v", errorValue)
	}
	t.Cleanup(func() { _ = database.Close() })
	migrationRunner := postgres.MigrationRunner{MigrationDirectoryPath: "../../migrations"}
	if errorValue := migrationRunner.ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("expected migrations to apply: %v", errorValue)
	}
	resetMemoryTables(t, database)
	facts := bluememopostgres.NewFactRepository(database.SQL)
	hasVector, errorValue := facts.HasVectorSearch(ctx)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return memoryStoreFixture{
		database:  database,
		facts:     facts,
		jobs:      bluememopostgres.NewJobRepository(database.SQL),
		now:       time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
		hasVector: hasVector,
	}
}

func resetMemoryTables(t *testing.T, database postgres.Database) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`TRUNCATE memory_job, memory_profile, memory_fact, memory_episode CASCADE`,
		`DELETE FROM person WHERE person_id LIKE 'memtest-%'`,
	} {
		if errorValue := database.Exec(ctx, statement); errorValue != nil {
			t.Fatalf("expected %q to run: %v", statement, errorValue)
		}
	}
}

func (fixture memoryStoreFixture) addPerson(t *testing.T, personID string) string {
	t.Helper()
	qualifiedPersonID := memoryTestPersonPrefix + personID
	errorValue := fixture.database.Exec(context.Background(), `
INSERT INTO person (person_id, display_name, security_level_name, security_level_rank, granted_classes, created_at, updated_at)
VALUES ($1, $1, 'member', 1, '{}', now(), now())
ON CONFLICT (person_id) DO NOTHING`, qualifiedPersonID)
	if errorValue != nil {
		t.Fatalf("expected the person to be inserted: %v", errorValue)
	}
	return qualifiedPersonID
}

func (fixture memoryStoreFixture) episode(requesterPersonID string) bluememo.Episode {
	return bluememo.Episode{
		EpisodeID:         taskstate.NewIdentifier(),
		SourceKind:        bluememo.EpisodeSourceKindTaskRun,
		SourceID:          taskstate.NewIdentifier(),
		RequesterPersonID: requesterPersonID,
		Content:           "transcript",
		OccurredAt:        fixture.now,
	}
}

func (fixture memoryStoreFixture) fact(episodeID string, scopeType string, scopeID string, content string) bluememo.Fact {
	fact := bluememo.Fact{
		FactID:        taskstate.NewIdentifier(),
		EpisodeID:     episodeID,
		OwnerPersonID: scopeID,
		Kind:          bluememo.FactKindFact,
		Content:       content,
		ValidFrom:     fixture.now.Add(-time.Hour),
	}
	switch scopeType {
	case "circle":
		fact.OwnerPersonID = "memtest-carol"
		fact.CircleIDs = []string{scopeID}
	case "member":
		fact.OwnerPersonID = "memtest-carol"
		fact.CircleIDs = []string{"member"}
	}
	return fact
}

func (fixture memoryStoreFixture) save(t *testing.T, episode bluememo.Episode, facts ...bluememo.FactWrite) {
	t.Helper()
	if errorValue := fixture.facts.SaveEpisode(context.Background(), bluememo.EpisodeWrite{Episode: episode, Facts: facts}); errorValue != nil {
		t.Fatalf("expected the episode to save: %v", errorValue)
	}
}

func (fixture memoryStoreFixture) search(t *testing.T, reader bluememo.Reader, text string) map[string]bluememo.RankedFact {
	t.Helper()
	hits, errorValue := fixture.facts.SearchFacts(context.Background(), bluememo.FactSearchQuery{Reader: reader, Text: text, ReferenceTime: fixture.now})
	if errorValue != nil {
		t.Fatalf("expected the search to run: %v", errorValue)
	}
	byContent := map[string]bluememo.RankedFact{}
	for _, hit := range hits {
		byContent[hit.Fact.Content] = hit
	}
	return byContent
}

func TestMemoryStoreReaderFilterGatesScopeRankAndClasses(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	alice := fixture.addPerson(t, "alice")
	bob := fixture.addPerson(t, "bob")
	episode := fixture.episode(alice)
	aliceFact := fixture.fact(episode.EpisodeID, "private", alice, "이샘플 owns the Q3 review 프로젝트")
	bobFact := fixture.fact(episode.EpisodeID, "private", bob, "박예시 owns the Q3 budget 프로젝트")
	circleFact := fixture.fact(episode.EpisodeID, "circle", "circle-platform", "the platform circle runs the Q3 프로젝트 retro")
	strangerCircleFact := fixture.fact(episode.EpisodeID, "circle", "circle-finance", "the finance circle closes the Q3 프로젝트 books")
	openFact := fixture.fact(episode.EpisodeID, "member", "", "the Q3 프로젝트 review is on 2026-09-20")
	secretFact := fixture.fact(episode.EpisodeID, "member", "", "the Q3 프로젝트 headcount plan is frozen")
	secretFact.SecurityLevelRank = 3
	classedFact := fixture.fact(episode.EpisodeID, "member", "", "the Q3 프로젝트 legal hold list")
	classedFact.RequiredClasses = []string{"legal"}
	fixture.save(t, episode,
		bluememo.FactWrite{Fact: aliceFact}, bluememo.FactWrite{Fact: bobFact}, bluememo.FactWrite{Fact: circleFact},
		bluememo.FactWrite{Fact: strangerCircleFact}, bluememo.FactWrite{Fact: openFact}, bluememo.FactWrite{Fact: secretFact},
		bluememo.FactWrite{Fact: classedFact},
	)

	reader := bluememo.NewReader(alice, []string{"member", "circle-platform"}, nil, 1, nil)
	hits := fixture.search(t, reader, "Q3 프로젝트")
	for _, visible := range []bluememo.Fact{aliceFact, circleFact, openFact} {
		if _, isVisible := hits[visible.Content]; !isVisible {
			t.Fatalf("expected %q to be readable, got %v", visible.Content, hits)
		}
	}
	for _, hidden := range []bluememo.Fact{bobFact, strangerCircleFact, secretFact, classedFact} {
		if _, isVisible := hits[hidden.Content]; isVisible {
			t.Fatalf("expected %q to be hidden from the reader", hidden.Content)
		}
	}
	if hits[openFact.Content].LexicalRank == 0 {
		t.Fatal("expected a lexical rank on every hit")
	}
}

func TestMemoryStoreSupersedeHidesTheOldFactButKeepsItsRow(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	alice := fixture.addPerson(t, "alice")
	firstEpisode := fixture.episode(alice)
	oldFact := fixture.fact(firstEpisode.EpisodeID, "private", alice, "이샘플 works at Google as an engineer")
	fixture.save(t, firstEpisode, bluememo.FactWrite{Fact: oldFact})

	secondEpisode := fixture.episode(alice)
	newFact := fixture.fact(secondEpisode.EpisodeID, "private", alice, "이샘플 works at Stripe as a product manager")
	fixture.save(t, secondEpisode, bluememo.FactWrite{Fact: newFact, SupersedesFactID: oldFact.FactID})

	reader := bluememo.NewReader(alice, nil, nil, 1, nil)
	hits := fixture.search(t, reader, "이샘플 works")
	if _, isVisible := hits[oldFact.Content]; isVisible {
		t.Fatal("expected the superseded fact to leave search")
	}
	if _, isVisible := hits[newFact.Content]; !isVisible {
		t.Fatalf("expected the new fact to be found, got %v", hits)
	}
	var supersededBy string
	if errorValue := fixture.database.SQL.QueryRow(`SELECT superseded_by FROM memory_fact WHERE fact_id = $1`, oldFact.FactID).Scan(&supersededBy); errorValue != nil {
		t.Fatal(errorValue)
	}
	if supersededBy != newFact.FactID {
		t.Fatalf("expected the old row to point at %s, got %q", newFact.FactID, supersededBy)
	}

	thirdEpisode := fixture.episode(alice)
	stale := fixture.fact(thirdEpisode.EpisodeID, "private", alice, "이샘플 works nowhere")
	if errorValue := fixture.facts.SaveEpisode(context.Background(), bluememo.EpisodeWrite{Episode: thirdEpisode, Facts: []bluememo.FactWrite{{Fact: stale, SupersedesFactID: oldFact.FactID}}}); errorValue == nil {
		t.Fatal("expected superseding an already superseded fact to fail")
	}
}

func TestMemoryStoreTemporaryFactsExpireAndReinforcementCounts(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	alice := fixture.addPerson(t, "alice")
	episode := fixture.episode(alice)
	expired := fixture.fact(episode.EpisodeID, "private", alice, "이샘플 is out of office until yesterday")
	expired.Kind, expired.ValidUntil = bluememo.FactKindTemporary, fixture.now.Add(-time.Minute)
	current := fixture.fact(episode.EpisodeID, "private", alice, "이샘플 is out of office until next week")
	current.Kind, current.ValidUntil = bluememo.FactKindTemporary, fixture.now.Add(7*24*time.Hour)
	preference := fixture.fact(episode.EpisodeID, "private", alice, "이샘플 prefers bullet summaries")
	preference.Kind = bluememo.FactKindPreference
	fixture.save(t, episode, bluememo.FactWrite{Fact: expired}, bluememo.FactWrite{Fact: current}, bluememo.FactWrite{Fact: preference})

	reader := bluememo.NewReader(alice, nil, nil, 1, nil)
	hits := fixture.search(t, reader, "out of office")
	if _, isVisible := hits[expired.Content]; isVisible {
		t.Fatal("expected the expired temporary fact to be hidden")
	}
	if _, isVisible := hits[current.Content]; !isVisible {
		t.Fatalf("expected the current temporary fact to be found, got %v", hits)
	}

	secondEpisode := fixture.episode(alice)
	fixture.save(t, secondEpisode, bluememo.FactWrite{ReinforcesFactID: preference.FactID})
	facts, errorValue := fixture.facts.ListFactsByID(context.Background(), reader, []string{preference.FactID}, fixture.now)
	if errorValue != nil || len(facts) != 1 || facts[0].ReinforcementCount != 2 {
		t.Fatalf("expected the preference to be reinforced to 2, got %+v (%v)", facts, errorValue)
	}
}

func TestMemoryStoreForgetIsScopedToTheReaderAndKeepsTheRow(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	alice := fixture.addPerson(t, "alice")
	bob := fixture.addPerson(t, "bob")
	episode := fixture.episode(alice)
	aliceFact := fixture.fact(episode.EpisodeID, "private", alice, "이샘플 parks on level 2")
	bobFact := fixture.fact(episode.EpisodeID, "private", bob, "박예시 parks on level 3")
	fixture.save(t, episode, bluememo.FactWrite{Fact: aliceFact}, bluememo.FactWrite{Fact: bobFact})

	reader := bluememo.NewReader(alice, nil, nil, 1, nil)
	forgotten, errorValue := fixture.facts.ForgetFacts(context.Background(), reader, []string{aliceFact.FactID, bobFact.FactID}, "moved desks", fixture.now)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(forgotten) != 1 || forgotten[0] != aliceFact.FactID {
		t.Fatalf("expected only the reader's own fact to be forgotten, got %v", forgotten)
	}
	if hits := fixture.search(t, reader, "parks on level"); len(hits) != 0 {
		t.Fatalf("expected the forgotten fact to leave search, got %v", hits)
	}
	var reason string
	if errorValue := fixture.database.SQL.QueryRow(`SELECT forget_reason FROM memory_fact WHERE fact_id = $1 AND forgotten_at IS NOT NULL`, aliceFact.FactID).Scan(&reason); errorValue != nil || reason != "moved desks" {
		t.Fatalf("expected the forgotten row to survive with its reason, got %q (%v)", reason, errorValue)
	}
}

func TestMemoryStoreVectorSearchRanksByEmbedding(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	if !fixture.hasVector {
		t.Skip("the test database has no vector extension")
	}
	alice := fixture.addPerson(t, "alice")
	episode := fixture.episode(alice)
	near := fixture.fact(episode.EpisodeID, "private", alice, "the standup moved to 10am")
	far := fixture.fact(episode.EpisodeID, "private", alice, "the parking garage closes at midnight")
	fixture.save(t, episode,
		bluememo.FactWrite{Fact: near, Embedding: unitEmbedding(0)},
		bluememo.FactWrite{Fact: far, Embedding: unitEmbedding(1)},
	)

	reader := bluememo.NewReader(alice, nil, nil, 1, nil)
	hits, errorValue := fixture.facts.SearchFacts(context.Background(), bluememo.FactSearchQuery{Reader: reader, Text: "zzzz", Embedding: unitEmbedding(0), ReferenceTime: fixture.now})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	ranks := map[string]int{}
	for _, hit := range hits {
		ranks[hit.Fact.FactID] = hit.VectorRank
	}
	if ranks[near.FactID] != 1 || ranks[far.FactID] != 2 {
		t.Fatalf("expected vector ranks near=1 far=2, got %v", ranks)
	}
}

func TestMemoryStoreJobsDeduplicateClaimAndSettle(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	ctx := context.Background()
	first, created, errorValue := fixture.jobs.EnqueueJob(ctx, bluememo.JobKindExtract, "task-run-1", fixture.now)
	if errorValue != nil || !created {
		t.Fatalf("expected the first enqueue to create a job, got created=%v (%v)", created, errorValue)
	}
	duplicate, created, errorValue := fixture.jobs.EnqueueJob(ctx, bluememo.JobKindExtract, "task-run-1", fixture.now)
	if errorValue != nil || created || duplicate.JobID != first.JobID {
		t.Fatalf("expected the duplicate enqueue to return the pending job, got created=%v job=%+v (%v)", created, duplicate, errorValue)
	}

	claimed, errorValue := fixture.jobs.ClaimDueJobs(ctx, []string{bluememo.JobKindExtract}, fixture.now, time.Minute, 10)
	if errorValue != nil || len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("expected one claim with attempts=1, got %+v (%v)", claimed, errorValue)
	}
	if again, _ := fixture.jobs.ClaimDueJobs(ctx, []string{bluememo.JobKindExtract}, fixture.now.Add(time.Second), time.Minute, 10); len(again) != 0 {
		t.Fatalf("expected the leased job to stay claimed, got %+v", again)
	}
	if errorValue := fixture.jobs.RetryJob(ctx, first.JobID, "model unavailable", fixture.now.Add(time.Minute)); errorValue != nil {
		t.Fatal(errorValue)
	}
	if early, _ := fixture.jobs.ClaimDueJobs(ctx, []string{bluememo.JobKindExtract}, fixture.now.Add(30*time.Second), time.Minute, 10); len(early) != 0 {
		t.Fatalf("expected the retried job to wait for run_after, got %+v", early)
	}
	retried, errorValue := fixture.jobs.ClaimDueJobs(ctx, []string{bluememo.JobKindExtract}, fixture.now.Add(2*time.Minute), time.Minute, 10)
	if errorValue != nil || len(retried) != 1 || retried[0].Attempts != 2 || retried[0].LastError != "model unavailable" {
		t.Fatalf("expected the retried job to be claimed with attempts=2, got %+v (%v)", retried, errorValue)
	}
	if errorValue := fixture.jobs.FinishJob(ctx, first.JobID, fixture.now.Add(3*time.Minute)); errorValue != nil {
		t.Fatal(errorValue)
	}
	_, created, errorValue = fixture.jobs.EnqueueJob(ctx, bluememo.JobKindExtract, "task-run-1", fixture.now)
	if errorValue != nil || !created {
		t.Fatalf("expected a finished subject to accept a new job, got created=%v (%v)", created, errorValue)
	}
}

func unitEmbedding(axis int) []float32 {
	embedding := make([]float32, bluememo.EmbeddingDimensionCount)
	embedding[axis] = 1
	return embedding
}
