package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const retiringMigrationName = "037_drop_the_retired_memory_store.sql"

const memoryLibrarySchema = `
CREATE TABLE IF NOT EXISTS memory_schema_migration (
  file_name text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS memory_fact_trigger (
  trigger_id text PRIMARY KEY,
  fact_id text NOT NULL REFERENCES memory_fact (fact_id) ON DELETE CASCADE,
  phrase text NOT NULL CHECK (char_length(phrase) BETWEEN 1 AND 80),
  embedding_model text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS memory_fact_trigger_phrase_idx
  ON memory_fact_trigger (fact_id, phrase);
CREATE TABLE IF NOT EXISTS memory_fact_trigger_embedding (
  trigger_id text PRIMARY KEY REFERENCES memory_fact_trigger (trigger_id) ON DELETE CASCADE,
  embedding real[] NOT NULL
);`

func TestTheRetiringMigrationDropsWhatTheMemoryLibraryMadeAtStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	administrator := administratorDatabase(t, ctx)
	database := databaseUnderAnOrdinaryOwner(t, ctx, administrator)
	if errorValue := (MigrationRunner{MigrationDirectoryPath: migrationsBefore(t, retiringMigrationName)}).ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("migrate up to %s: %v", retiringMigrationName, errorValue)
	}
	if errorValue := database.Exec(ctx, memoryLibrarySchema); errorValue != nil {
		t.Fatalf("make the memory library's own tables: %v", errorValue)
	}
	if errorValue := (MigrationRunner{MigrationDirectoryPath: "../../../migrations"}).ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("a database the memory library migrated at startup did not migrate to the end: %v", errorValue)
	}
	var standing []string
	rows, errorValue := database.SQL.QueryContext(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename LIKE 'memory\_%' ORDER BY tablename`)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer rows.Close()
	for rows.Next() {
		var tableName string
		if errorValue := rows.Scan(&tableName); errorValue != nil {
			t.Fatal(errorValue)
		}
		standing = append(standing, tableName)
	}
	if len(standing) > 0 {
		t.Fatalf("%v are left standing, and memory no longer lives in Postgres", standing)
	}
}

func migrationsBefore(t *testing.T, firstLeftOut string) string {
	t.Helper()
	directoryPath := t.TempDir()
	migrationPaths, errorValue := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "*.sql"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, migrationPath := range migrationPaths {
		if filepath.Base(migrationPath) >= firstLeftOut {
			continue
		}
		document, errorValue := os.ReadFile(migrationPath)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if errorValue := os.WriteFile(filepath.Join(directoryPath, filepath.Base(migrationPath)), document, 0o600); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	return directoryPath
}
