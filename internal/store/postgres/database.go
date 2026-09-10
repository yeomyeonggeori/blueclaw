package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Database struct {
	ConnectionString string
	SQL              *sql.DB
}

func OpenDatabase(ctx context.Context, connectionString string) (Database, error) {
	if strings.TrimSpace(connectionString) == "" {
		return Database{}, errors.New("postgres connection string is required")
	}
	sqlDatabase, errorValue := sql.Open("pgx", connectionString)
	if errorValue != nil {
		return Database{}, errorValue
	}
	if errorValue := sqlDatabase.PingContext(ctx); errorValue != nil {
		_ = sqlDatabase.Close()
		return Database{}, errorValue
	}
	return Database{ConnectionString: connectionString, SQL: sqlDatabase}, nil
}

func (database Database) Close() error {
	if database.SQL == nil {
		return nil
	}
	return database.SQL.Close()
}

func (database Database) Exec(ctx context.Context, query string, arguments ...any) error {
	if database.SQL == nil {
		return errors.New("postgres database is not open")
	}
	_, errorValue := database.SQL.ExecContext(ctx, query, arguments...)
	return errorValue
}

func (migrationRunner MigrationRunner) ApplyMigrations(ctx context.Context, database Database) error {
	migrationPaths, errorValue := migrationRunner.ListMigrationPath()
	if errorValue != nil {
		return errorValue
	}
	migrationRunner.log("migration.phase", "phase", "listed", "count", len(migrationPaths))
	if errorValue := database.ensureMigrationHistory(ctx); errorValue != nil {
		return errorValue
	}
	migrationRunner.log("migration.phase", "phase", "history_ready")
	appliedMigrationFileNames, errorValue := database.appliedMigrationFileNames(ctx)
	if errorValue != nil {
		return errorValue
	}
	migrationRunner.log("migration.phase", "phase", "history_loaded", "count", len(appliedMigrationFileNames))
	if len(appliedMigrationFileNames) == 0 {
		hasCurrentSchema, errorValue := database.hasCurrentSchema(ctx)
		if errorValue != nil {
			return errorValue
		}
		migrationRunner.log("migration.phase", "phase", "current_schema_checked", "hasCurrentSchema", hasCurrentSchema)
		if hasCurrentSchema {
			migrationRunner.log("migration.phase", "phase", "baseline_start")
			return migrationRunner.baselineMigrations(ctx, database, migrationPaths)
		}
	}
	for _, migrationPath := range migrationPaths {
		fileName := filepath.Base(migrationPath)
		if appliedMigrationFileNames[fileName] {
			migrationRunner.log("migration.file", "file", fileName, "action", "skip")
			continue
		}
		migrationRunner.log("migration.file", "file", fileName, "action", "read")
		document, errorValue := os.ReadFile(migrationPath)
		if errorValue != nil {
			return errorValue
		}
		if strings.TrimSpace(string(document)) == "" {
			migrationRunner.log("migration.file", "file", fileName, "action", "record_empty")
			if errorValue := database.recordMigration(ctx, fileName); errorValue != nil {
				return errorValue
			}
			continue
		}
		migrationRunner.log("migration.file", "file", fileName, "action", "apply")
		if errorValue := database.Exec(ctx, string(document)); errorValue != nil {
			return fmt.Errorf("apply migration %s: %w", fileName, errorValue)
		}
		migrationRunner.log("migration.file", "file", fileName, "action", "record")
		if errorValue := database.recordMigration(ctx, fileName); errorValue != nil {
			return errorValue
		}
	}
	migrationRunner.log("migration.phase", "phase", "done")
	return nil
}

func (database Database) ensureMigrationHistory(ctx context.Context) error {
	return database.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migration (
  file_name text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
)`)
}

func (database Database) appliedMigrationFileNames(ctx context.Context) (map[string]bool, error) {
	rows, errorValue := database.SQL.QueryContext(ctx, `
SELECT file_name
FROM schema_migration`)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	fileNames := map[string]bool{}
	for rows.Next() {
		var fileName string
		if errorValue := rows.Scan(&fileName); errorValue != nil {
			return nil, errorValue
		}
		fileNames[fileName] = true
	}
	return fileNames, rows.Err()
}

func (database Database) hasCurrentSchema(ctx context.Context) (bool, error) {
	row := database.SQL.QueryRowContext(ctx, `
SELECT
  to_regclass('public.person') IS NOT NULL
  AND to_regclass('public.raw_event') IS NOT NULL
  AND to_regclass('public.connector_outbox') IS NOT NULL
  AND to_regclass('public.task_attempt') IS NOT NULL
  AND to_regclass('public.memory_fact') IS NOT NULL
  AND to_regclass('public.graphiti_episode') IS NULL`)
	var hasCurrentSchema bool
	errorValue := row.Scan(&hasCurrentSchema)
	return hasCurrentSchema, errorValue
}

func (migrationRunner MigrationRunner) baselineMigrations(ctx context.Context, database Database, migrationPaths []string) error {
	for _, migrationPath := range migrationPaths {
		migrationRunner.log("migration.file", "file", filepath.Base(migrationPath), "action", "baseline_record")
		if errorValue := database.recordMigration(ctx, filepath.Base(migrationPath)); errorValue != nil {
			return errorValue
		}
	}
	migrationRunner.log("migration.phase", "phase", "baseline_done")
	return nil
}

func (database Database) recordMigration(ctx context.Context, fileName string) error {
	return database.Exec(ctx, `
INSERT INTO schema_migration (file_name)
VALUES ($1)
ON CONFLICT (file_name) DO NOTHING`, fileName)
}

func (migrationRunner MigrationRunner) log(message string, arguments ...any) {
	if migrationRunner.Logger == nil {
		return
	}
	migrationRunner.Logger.Info(message, arguments...)
}
