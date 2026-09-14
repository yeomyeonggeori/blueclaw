package integration

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/migrations"
	bluememopostgres "github.com/yeomyeonggeori/bluememo/postgres"
)

func TestMemoryLibraryMigrationsRunThroughHostStartup(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	ctx := context.Background()

	memoryMigrations, errorValue := migrations.List()
	if errorValue != nil {
		t.Fatalf("expected the memory migrations to list: %v", errorValue)
	}
	rows, errorValue := fixture.database.SQL.QueryContext(ctx, `SELECT file_name FROM memory_schema_migration`)
	if errorValue != nil {
		t.Fatalf("expected the memory migration ledger to be queryable: %v", errorValue)
	}
	defer rows.Close()
	completedMigrationNames := map[string]bool{}
	for rows.Next() {
		var migrationName string
		if errorValue := rows.Scan(&migrationName); errorValue != nil {
			t.Fatalf("expected a migration name to scan: %v", errorValue)
		}
		completedMigrationNames[migrationName] = true
	}
	if errorValue := rows.Err(); errorValue != nil {
		t.Fatalf("expected migration ledger rows to finish: %v", errorValue)
	}
	for _, migration := range memoryMigrations {
		if !completedMigrationNames[migration.Name] {
			t.Errorf("expected memory migration %q in the host startup ledger", migration.Name)
		}
	}

	personID := fixture.addPerson(t, "migration")
	episode := fixture.episode(personID)
	fact := fixture.fact(episode.EpisodeID, "private", personID, "a migration-backed source fact")
	if errorValue := fixture.facts.SaveEpisode(ctx, bluememo.EpisodeWrite{
		Episode:        episode,
		Facts:          []bluememo.FactWrite{{Fact: fact}},
		CandidateCount: 1,
	}); errorValue != nil {
		t.Fatalf("expected the episode and receipt to save: %v", errorValue)
	}
	receipt, isFound, errorValue := fixture.facts.FindEpisodeReceipt(ctx, episode)
	if errorValue != nil || !isFound || receipt.CandidateCount != 1 || len(receipt.FactIDs) != 1 || receipt.FactIDs[0] != fact.FactID {
		t.Fatalf("expected the episode receipt to round-trip, got %+v found=%v (%v)", receipt, isFound, errorValue)
	}

	profileRepository := bluememopostgres.NewProfileRepository(fixture.database.SQL)
	profile := bluememo.Profile{
		PersonID:           personID,
		IdentityLines:      []string{"the person has a migration-backed profile"},
		CurrentLines:       []string{},
		SourceFactIDs:      []string{fact.FactID},
		BuiltFromFactCount: 1,
		BuiltAt:            fixture.now,
	}
	if errorValue := profileRepository.SaveProfile(ctx, profile); errorValue != nil {
		t.Fatalf("expected the profile with source IDs to save: %v", errorValue)
	}
	storedProfile, isFound, errorValue := profileRepository.FindProfile(ctx, personID)
	if errorValue != nil || !isFound || storedProfile.BuiltFromFactCount != 1 || len(storedProfile.SourceFactIDs) != 1 || storedProfile.SourceFactIDs[0] != fact.FactID {
		t.Fatalf("expected profile source IDs to round-trip, got %+v found=%v (%v)", storedProfile, isFound, errorValue)
	}

	job, isCreated, errorValue := fixture.jobs.EnqueueJob(ctx, bluememo.JobKindExtract, "migration-run", fixture.now)
	if errorValue != nil || !isCreated {
		t.Fatalf("expected an extraction job to enqueue, got created=%v (%v)", isCreated, errorValue)
	}
	claimedJobs, errorValue := fixture.jobs.ClaimDueJobs(ctx, []string{bluememo.JobKindExtract}, fixture.now, time.Minute, 1)
	if errorValue != nil || len(claimedJobs) != 1 || claimedJobs[0].JobID != job.JobID || claimedJobs[0].Generation < 1 || claimedJobs[0].ClaimToken == "" {
		t.Fatalf("expected the claim token and generation to round-trip, got %+v (%v)", claimedJobs, errorValue)
	}
}
