package postgres

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const oneWriterMigrationName = "041_one_writer_per_event_name.sql"

var eventsStoredUnderTheOldNames = []storedEvent{
	{"run-ask-input", "run-w", "ask.requested", `{"kind":"ask_input","question":"which one?"}`},
	{"run-ask-confirm", "run-w", "ask.requested", `{"kind":"ask_confirm","message":"delete it?"}`},
	{"run-delivery-report", "run-w", "agent.failure_report", `{"phase":"delivery","report":{},"generation":{}}`},
	{"run-loop-report", "run-w", "agent.failure_report", `{"phase":"failure","report":{},"generation":{}}`},
	{"run-launch-report", "run-w", "agent.failure_report", `{"phase":"launch","report":{},"generation":{}}`},
	{"run-host-suppressed", "run-w", "task.stop.outbox_suppressed", `{"messageID":"m-1","reason":"task was cancelled before final reply send"}`},
	{"run-loop-suppressed", "run-w", "task.stop.outbox_suppressed", `task run was cancelled before reply delivery`},
	{"run-limit-stop", "run-w", "agent.limit_stop", `{"phase":"intake"}`},
	{"run-blocked-goal", "run-w", "agent.goal.blocked", `{"goalID":"run-w","status":"blocked"}`},
}

func TestTheOneWriterMigrationRenamesOnlyTheRowsWhoseWriterIsKnownAndKeepsAsksVisible(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database := databaseUnderAnOrdinaryOwner(t, ctx, administratorDatabase(t, ctx))
	migrateBefore(t, ctx, database, oneWriterMigrationName)
	if errorValue := database.Exec(ctx, `INSERT INTO task_run (task_run_id, current_agent_profile_name, status, prompt, created_at, updated_at) VALUES ('run-w', 'assistant', 'waiting_user_input', 'fixture', now(), now())`); errorValue != nil {
		t.Fatal(errorValue)
	}
	for index, event := range eventsStoredUnderTheOldNames {
		if errorValue := database.Exec(ctx, `INSERT INTO task_event (task_event_id, task_run_id, name, body, created_at) VALUES ($1, $2, $3, $4, $5)`, event.id, event.run, event.name, event.body, time.Now().Add(-time.Hour+time.Duration(index)*time.Second)); errorValue != nil {
			t.Fatal(errorValue)
		}
	}

	runner := MigrationRunner{MigrationDirectoryPath: filepath.Join("..", "..", "..", "migrations")}
	if errorValue := runner.ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("the migration did not apply over old rows: %v", errorValue)
	}
	migrated := storedEventsByID(t, ctx, database)

	expectedNames := map[string]string{
		"run-ask-input":       "ask.requested",
		"run-ask-confirm":     "ask.requested",
		"run-delivery-report": task.TaskEventConnectorFilesUndelivered,
		"run-loop-report":     "agent.failure_report",
		"run-launch-report":   "agent.failure_report",
		"run-host-suppressed": task.TaskEventConnectorStopOutboxSuppressed,
		"run-loop-suppressed": "task.stop.outbox_suppressed",
		"run-limit-stop":      "agent.limit_stop",
		"run-blocked-goal":    "agent.goal.blocked",
	}
	for id, expectedName := range expectedNames {
		if migrated[id].name != expectedName {
			t.Fatalf("%s is stored as %q, expected %q", id, migrated[id].name, expectedName)
		}
		if migrated[id].body != findOldEvent(id).body {
			t.Fatalf("%s lost its body: %q", id, migrated[id].body)
		}
	}
	for _, askID := range []string{"run-ask-input", "run-ask-confirm"} {
		if !task.IsAskRequestedEvent(migrated[askID].name) {
			t.Fatalf("%s no longer reads as an ask, so a pending question would be missed across the deploy", askID)
		}
	}

	again := database.Exec(ctx, readMigration(t, oneWriterMigrationName))
	if again != nil || !sameStoredEvents(migrated, storedEventsByID(t, ctx, database)) {
		t.Fatalf("running the migration a second time changed rows or failed: %v", again)
	}
}

func findOldEvent(id string) storedEvent {
	for _, event := range eventsStoredUnderTheOldNames {
		if event.id == id {
			return event
		}
	}
	return storedEvent{}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	document, errorValue := os.ReadFile(filepath.Join("..", "..", "..", "migrations", name))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(document)
}

func migrateBefore(t *testing.T, ctx context.Context, database Database, migrationName string) {
	t.Helper()
	migrationsDirectory := filepath.Join("..", "..", "..", "migrations")
	entries, errorValue := os.ReadDir(migrationsDirectory)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	earlierDirectory := t.TempDir()
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= migrationName {
			continue
		}
		if errorValue := os.WriteFile(filepath.Join(earlierDirectory, entry.Name()), []byte(readMigration(t, entry.Name())), 0o600); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if errorValue := (MigrationRunner{MigrationDirectoryPath: earlierDirectory}).ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("the schema before the migration did not apply: %v", errorValue)
	}
}
