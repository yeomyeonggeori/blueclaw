package postgres

import (
	"context"
	"testing"
)

func TestAStoreMigratedBeforeVectorExistedGainsItsEmbeddingsOnceVectorDoes(t *testing.T) {
	ctx := context.Background()
	database, closeDatabase := morningBriefingIntegrationDatabase(t, ctx)
	defer closeDatabase()
	state, errorValue := database.memoryEmbeddingState(ctx)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !state.isVectorCreated {
		t.Skip("this Postgres offers no vector extension")
	}
	if errorValue := database.Exec(ctx, `DROP TABLE memory_fact_embedding`); errorValue != nil {
		t.Fatal(errorValue)
	}

	if errorValue := (MigrationRunner{MigrationDirectoryPath: "../../../migrations"}).ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatal(errorValue)
	}

	state, errorValue = database.memoryEmbeddingState(ctx)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !state.hasEmbeddingTable {
		t.Fatal("vector exists and the memory store still has no embedding table, so recall matches words alone for good")
	}
}
