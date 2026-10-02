package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// Memory is one SQLite file per subject now. The tables it used to live in are
// dropped, and a migration that brings one back would give the store a second
// home that nothing reads and the reset would fail on.
func TestTheRetiredMemoryTablesStayDropped(t *testing.T) {
	live := liveTableNames(t)
	for _, tableName := range []string{
		"memory_episode", "memory_fact", "memory_fact_circle",
		"memory_fact_embedding", "memory_profile", "memory_job",
	} {
		if live[tableName] {
			t.Errorf("%s is left standing by the migrations, and memory no longer lives in Postgres", tableName)
		}
	}
}

// liveTableNames follows every create, drop and rename in migration order and
// returns what is left standing.
func liveTableNames(t *testing.T) map[string]bool {
	t.Helper()
	migrationPaths, errorValue := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "*.sql"))
	if errorValue != nil || len(migrationPaths) == 0 {
		t.Fatalf("no migrations to read: %v", errorValue)
	}
	sort.Strings(migrationPaths)
	created := regexp.MustCompile(`(?i)CREATE TABLE (?:IF NOT EXISTS )?([a-z_][a-z0-9_]*)`)
	dropped := regexp.MustCompile(`(?i)DROP TABLE (?:IF EXISTS )?([a-z_][a-z0-9_]*)`)
	renamed := regexp.MustCompile(`(?i)ALTER TABLE ([a-z_][a-z0-9_]*) RENAME TO ([a-z_][a-z0-9_]*)`)
	live := map[string]bool{}
	for _, migrationPath := range migrationPaths {
		body, errorValue := os.ReadFile(migrationPath)
		if errorValue != nil {
			t.Fatalf("read %s: %v", migrationPath, errorValue)
		}
		for _, match := range created.FindAllStringSubmatch(string(body), -1) {
			live[match[1]] = true
		}
		for _, match := range dropped.FindAllStringSubmatch(string(body), -1) {
			live[match[1]] = false
		}
		for _, match := range renamed.FindAllStringSubmatch(string(body), -1) {
			live[match[1]] = false
			live[match[2]] = true
		}
	}
	return live
}
