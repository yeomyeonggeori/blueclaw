package postgres

import (
	"context"
	"fmt"

	bluememomigrations "github.com/yeomyeonggeori/bluememo/migrations"
)

const memoryStoreMigrationName = "001_memory_store.sql"

type memoryEmbeddingState struct {
	isVectorCreated   bool
	isVectorAvailable bool
	hasEmbeddingTable bool
}

func (database Database) memoryEmbeddingState(ctx context.Context) (memoryEmbeddingState, error) {
	state := memoryEmbeddingState{}
	errorValue := database.SQL.QueryRowContext(ctx, `
SELECT
  EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector'),
  EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector'),
  to_regclass('public.memory_fact_embedding') IS NOT NULL`).Scan(
		&state.isVectorCreated, &state.isVectorAvailable, &state.hasEmbeddingTable)
	return state, errorValue
}

func (migrationRunner MigrationRunner) ensureMemoryEmbeddings(ctx context.Context, database Database) error {
	state, errorValue := database.memoryEmbeddingState(ctx)
	if errorValue != nil {
		return fmt.Errorf("read whether memory recall can search by meaning: %w", errorValue)
	}
	if state.hasEmbeddingTable {
		return nil
	}
	if !state.isVectorCreated {
		migrationRunner.warn("memory.embeddings.unavailable",
			"isVectorAvailable", state.isVectorAvailable,
			"consequence", "memory recall matches words alone",
			"remedy", "create the vector extension in this database as a superuser; the company host's install does")
		return nil
	}
	memoryStore, errorValue := memoryStoreMigration()
	if errorValue != nil {
		return errorValue
	}
	if errorValue := database.Exec(ctx, memoryStore); errorValue != nil {
		return fmt.Errorf("create the memory embedding table now that vector exists: %w", errorValue)
	}
	migrationRunner.log("memory.embeddings.created", "migration", memoryStoreMigrationName)
	return nil
}

func memoryStoreMigration() (string, error) {
	migrations, errorValue := bluememomigrations.List()
	if errorValue != nil {
		return "", errorValue
	}
	for _, migration := range migrations {
		if migration.Name == memoryStoreMigrationName {
			return migration.SQL, nil
		}
	}
	return "", fmt.Errorf("the memory library carries no %s, which is where its embedding table is declared", memoryStoreMigrationName)
}
