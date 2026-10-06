package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const holdVocabularyMigrationName = "040_one_hold_vocabulary.sql"

var runsWaitingForApproval = map[string]bool{"run-e": true}

type storedEvent struct {
	id   string
	run  string
	name string
	body string
}

var oldShapeEvents = []storedEvent{
	{"a-opened", "run-a", "approval.pending_call", `{"holdID":"hold-a","toolName":"event_delete","toolInput":{"eventID":"event-1"},"confirmation":"Delete it?"}`},
	{"a-offered", "run-a", "approval.choices_offered", `{"toolName":"event_delete","toolInput":{"eventID":"event-1"},"choices":[{"key":"now"},{"key":"offHours","startsAt":"2099-10-03T03:00:00+09:00"}]}`},
	{"a-minted", "run-a", "approval.held_call", `{"approvalToken":"loop-token-1","toolName":"event_delete","toolInput":{"eventID":"event-1"},"observationID":"obs-1"}`},
	{"a-decided", "run-a", "approval.decided", `{"holdID":"hold-a","decision":"confirm","source":"chat_reply"}`},
	{"a-spent", "run-a", "approval.executed", `{"approvalToken":"loop-token-1","holdID":"hold-a","toolName":"event_delete","toolInput":{"eventID":"event-1"}}`},
	{"b-opened", "run-b", "approval.pending_call", `{"toolName":"shell","toolInput":{"command":"pwd"},"confirmation":"Run it?"}`},
	{"b-decided", "run-b", "approval.decided", `{"holdID":"b-opened","decision":"cancel","source":"terminal"}`},
	{"b-drift", "run-b", "approval.unheld_call_carried_out", `{"toolName":"shell","toolInputKey":"shell","presentedToken":"loop-token-2","awaitingHeldCallIDs":["obs-1"]}`},
	{"c-opened", "run-c", "approval.pending_call", `{"approvalToken":"hold-c","toolName":"message_send","toolInput":{"to":["alice"]},"confirmation":"Send it?"}`},
	{"c-decided", "run-c", "approval.decided", `{"approvalToken":"hold-c","decision":"confirm_task","source":"chat_reply"}`},
	{"d-opened", "run-d", "approval.pending_call", `{"holdID":"hold-d","toolName":"event_delete","toolInput":{"eventID":"event-1"}}`},
	{"d-offered", "run-d", "approval.choices_offered", `{"toolName":"event_update","toolInput":{"eventID":"event-1"},"choices":[{"key":"now"}]}`},
	{"e-minted", "run-e", "approval.held_call", `{"approvalToken":"loop-token-3","toolName":"message_send","toolInput":{"to":["bob"]},"confirmation":"Send it?"}`},
	{"g-minted", "run-g", "approval.held_call", `{"approvalToken":"loop-token-4","toolName":"message_send","toolInput":{"to":["bob"]},"confirmation":"Send it?"}`},
	{"g-spent", "run-g", "approval.executed", `{"approvalToken":"loop-token-4","toolName":"message_send","toolInput":{"to":["bob"]}}`},
	{"l-open-1", "run-l", "approval.pending_call", `{"toolName":"event_delete","toolInput":{"eventID":"event-1"},"confirmation":"Delete 1?"}`},
	{"l-open-2", "run-l", "approval.pending_call", `{"toolName":"event_delete","toolInput":{"eventID":"event-2"},"confirmation":"Delete 2?"}`},
	{"l-decided-1", "run-l", "approval.decided", `{"decision":"cancel","source":"chat_reply"}`},
	{"l-decided-2", "run-l", "approval.decided", `{"decision":"confirm","source":"chat_reply"}`},
	{"l-spent", "run-l", "approval.executed", `{"toolName":"event_delete","toolInput":{"eventID":"event-1"}}`},
	{"f-paused", "run-f", "task.paused", `Send the message to someone?`},
}

func TestTheHoldVocabularyMigrationRewritesStoredApprovalEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database := databaseUnderAnOrdinaryOwner(t, ctx, administratorDatabase(t, ctx))
	migrateBeforeTheHoldVocabulary(t, ctx, database)
	insertOldShapeEvents(t, ctx, database)

	applyHoldVocabularyMigration(t, ctx, database)
	migrated := storedEventsByID(t, ctx, database)

	assertMigratedShape(t, migrated)
	assertMigratedHoldsReadBack(t, ctx, database)

	execMigrationFile(t, ctx, database)
	if again := storedEventsByID(t, ctx, database); !sameStoredEvents(migrated, again) {
		t.Fatalf("running the migration a second time changed rows:\nfirst  %v\nsecond %v", migrated, again)
	}
}

func migrateBeforeTheHoldVocabulary(t *testing.T, ctx context.Context, database Database) {
	t.Helper()
	migrationsDirectory := filepath.Join("..", "..", "..", "migrations")
	entries, errorValue := os.ReadDir(migrationsDirectory)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	earlierDirectory := t.TempDir()
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= holdVocabularyMigrationName {
			continue
		}
		document, errorValue := os.ReadFile(filepath.Join(migrationsDirectory, entry.Name()))
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if errorValue := os.WriteFile(filepath.Join(earlierDirectory, entry.Name()), document, 0o600); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if errorValue := (MigrationRunner{MigrationDirectoryPath: earlierDirectory}).ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("the schema before the migration did not apply: %v", errorValue)
	}
}

func insertOldShapeEvents(t *testing.T, ctx context.Context, database Database) {
	t.Helper()
	insertedRuns := map[string]bool{}
	createdAt := time.Now().Add(-time.Hour)
	for index, event := range oldShapeEvents {
		if !insertedRuns[event.run] {
			insertedRuns[event.run] = true
			if errorValue := database.Exec(ctx, `
INSERT INTO task_run (task_run_id, current_agent_profile_name, status, prompt, created_at, updated_at)
VALUES ($1, 'assistant', $2, 'hold vocabulary fixture', now(), now())`, event.run, statusOfFixtureRun(event.run)); errorValue != nil {
				t.Fatal(errorValue)
			}
		}
		if errorValue := database.Exec(ctx, `
INSERT INTO task_event (task_event_id, task_run_id, name, body, created_at)
VALUES ($1, $2, $3, $4, $5)`, event.id, event.run, event.name, event.body, createdAt.Add(time.Duration(index)*time.Second)); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
}

func statusOfFixtureRun(run string) string {
	if runsWaitingForApproval[run] {
		return "waiting_approval"
	}
	return "completed"
}

func applyHoldVocabularyMigration(t *testing.T, ctx context.Context, database Database) {
	t.Helper()
	runner := MigrationRunner{MigrationDirectoryPath: filepath.Join("..", "..", "..", "migrations")}
	if errorValue := runner.ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("the migration did not apply over old-shape rows: %v", errorValue)
	}
}

func execMigrationFile(t *testing.T, ctx context.Context, database Database) {
	t.Helper()
	document, errorValue := os.ReadFile(filepath.Join("..", "..", "..", "migrations", holdVocabularyMigrationName))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := database.Exec(ctx, string(document)); errorValue != nil {
		t.Fatalf("the migration is not safe to run twice: %v", errorValue)
	}
}

func storedEventsByID(t *testing.T, ctx context.Context, database Database) map[string]storedEvent {
	t.Helper()
	rows, errorValue := database.SQL.QueryContext(ctx, `SELECT task_event_id, task_run_id, name, body FROM task_event WHERE task_run_id LIKE 'run-%'`)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer rows.Close()
	events := map[string]storedEvent{}
	for rows.Next() {
		event := storedEvent{}
		if errorValue := rows.Scan(&event.id, &event.run, &event.name, &event.body); errorValue != nil {
			t.Fatal(errorValue)
		}
		events[event.id] = event
	}
	return events
}

func sameStoredEvents(first map[string]storedEvent, second map[string]storedEvent) bool {
	if len(first) != len(second) {
		return false
	}
	for id, event := range first {
		if second[id] != event {
			return false
		}
	}
	return true
}

func bodyOf(t *testing.T, event storedEvent) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if errorValue := json.Unmarshal([]byte(event.body), &decoded); errorValue != nil {
		t.Fatalf("event %s is not a JSON object after the migration: %q", event.id, event.body)
	}
	return decoded
}

func assertMigratedShape(t *testing.T, migrated map[string]storedEvent) {
	t.Helper()
	for _, goneID := range []string{"a-offered", "a-minted", "d-offered", "g-minted"} {
		if _, isPresent := migrated[goneID]; isPresent {
			t.Fatalf("%s should be folded into its hold or deleted, and it is still stored", goneID)
		}
	}
	expectedNames := map[string]string{
		"a-opened": "approval.hold_opened", "a-spent": "approval.hold_spent",
		"b-opened": "approval.hold_opened", "c-opened": "approval.hold_opened",
		"d-opened": "approval.hold_opened", "e-minted": "approval.hold_opened", "g-spent": "approval.hold_spent",
		"l-open-1": "approval.hold_opened", "l-open-2": "approval.hold_opened", "l-decided-1": "approval.decided",
		"l-decided-2": "approval.decided", "l-spent": "approval.hold_spent",
		"a-decided": "approval.decided", "b-decided": "approval.decided", "c-decided": "approval.decided",
		"b-drift": "approval.unheld_call_carried_out", "f-paused": "task.paused",
	}
	for id, expectedName := range expectedNames {
		if migrated[id].name != expectedName {
			t.Fatalf("event %s is named %q, expected %q", id, migrated[id].name, expectedName)
		}
	}
	for id, event := range migrated {
		if strings.Contains(event.body, "approvalToken") && event.name != "task.paused" {
			t.Fatalf("event %s still carries approvalToken: %s", id, event.body)
		}
	}
	expectedHoldIDs := map[string]string{
		"a-opened": "hold-a", "a-decided": "hold-a", "a-spent": "hold-a",
		"b-opened": "b-opened", "b-decided": "b-opened",
		"c-opened": "hold-c", "c-decided": "hold-c", "d-opened": "hold-d",
		"e-minted": "loop-token-3", "g-spent": "loop-token-4",
		"l-open-1": "l-open-1", "l-open-2": "l-open-2",
		"l-decided-1": "l-open-2", "l-decided-2": "l-open-1", "l-spent": "l-open-1",
	}
	for id, expectedHoldID := range expectedHoldIDs {
		if actual := bodyOf(t, migrated[id])["holdID"]; actual != expectedHoldID {
			t.Fatalf("event %s has holdID %v, expected %q: %s", id, actual, expectedHoldID, migrated[id].body)
		}
	}
	expectedDecisions := map[string]string{"a-decided": "approve", "b-decided": "reject", "c-decided": "approve", "l-decided-1": "reject", "l-decided-2": "approve"}
	for id, expectedDecision := range expectedDecisions {
		if actual := bodyOf(t, migrated[id])["decision"]; actual != expectedDecision {
			t.Fatalf("event %s decided %v, expected %q", id, actual, expectedDecision)
		}
	}
	drift := bodyOf(t, migrated["b-drift"])
	if drift["presentedHoldID"] != "loop-token-2" || drift["presentedToken"] != nil || drift["awaitingHeldCallIDs"] != nil {
		t.Fatalf("the drift record keeps its old keys: %s", migrated["b-drift"].body)
	}
	if migrated["f-paused"].body != `Send the message to someone?` {
		t.Fatalf("a body that is not JSON must be left as it was: %q", migrated["f-paused"].body)
	}
	if _, isCarried := bodyOf(t, migrated["d-opened"])["choices"]; isCarried {
		t.Fatalf("choices offered for another call must not attach to this hold: %s", migrated["d-opened"].body)
	}
}

func assertMigratedHoldsReadBack(t *testing.T, ctx context.Context, database Database) {
	t.Helper()
	repository := NewTaskEventRepository(database)
	holdsOf := func(runID string) []holdrecord.Hold {
		taskEvents, errorValue := repository.ListTaskEvent(runID)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		return holdrecord.Holds(taskEvents)
	}
	holdA := holdsOf("run-a")
	if len(holdA) != 1 || holdA[0].ID != "hold-a" || holdA[0].State != holdrecord.StateSpent || len(holdA[0].Choices) != 2 || holdA[0].Choices[1].StartsAt == "" {
		t.Fatalf("run-a should read back as one spent hold carrying its two choices, got %+v", holdA)
	}
	holdB := holdsOf("run-b")
	if len(holdB) != 1 || holdB[0].ID != "b-opened" || holdB[0].State != holdrecord.StateRejected {
		t.Fatalf("run-b should read back as a rejected hold known by its event id, got %+v", holdB)
	}
	holdC := holdsOf("run-c")
	if len(holdC) != 1 || holdC[0].ID != "hold-c" || holdC[0].State != holdrecord.StateApproved {
		t.Fatalf("run-c should read back as an approved hold, got %+v", holdC)
	}
	holdE := holdsOf("run-e")
	if len(holdE) != 1 || holdE[0].ID != "loop-token-3" || holdE[0].State != holdrecord.StatePending {
		t.Fatalf("run-e is still waiting and kept only the loop's record, which becomes its pending hold, got %+v", holdE)
	}
	if holdG := holdsOf("run-g"); len(holdG) != 0 {
		t.Fatalf("run-g finished, so the loop's record must not become a hold, got %+v", holdG)
	}
	holdL := holdsOf("run-l")
	if len(holdL) != 2 || holdL[0].ID != "l-open-1" || holdL[0].State != holdrecord.StateSpent || holdL[1].ID != "l-open-2" || holdL[1].State != holdrecord.StateRejected {
		t.Fatalf("run-l was written before hold ids: its decisions and spend must settle the holds, not leave them pending, got %+v", holdL)
	}
}
