package postgres

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const askShapeMigrationName = "042_one_ask_interaction_shape.sql"

var asksStoredInTheOldShapes = []storedEvent{
	{"confirm", "run-s", "ask.requested", `{"kind":"confirm","message":"Proceed?"}`},
	{"single-choices", "run-s", "ask.requested", `{"kind":"choice_single","question":"Which?","choices":["A"," ","B"]}`},
	{"multiple-choices", "run-s", "agent.input_requested", `{"kind":"choice_multiple","question":"Which?","choices":["A","B"]}`},
	{"input", "run-s", "ask.requested", `{"kind":"input","message":"Name?"}`},
	{"input-choice", "run-s", "ask.requested", `{"kind":"input_choice","question":"Pick","options":[{"key":"x","label":"X","value":"x"}],"choices":["ignored"]}`},
	{"current-input", "run-s", "ask.requested", `{"kind":"ask_input","question":"Q","message":"Q","options":[{"key":"1","label":"A","value":"A"}],"selectionMode":"single"}`},
	{"current-confirm", "run-s", "ask.requested", `{"kind":"ask_confirm","message":"M","question":"M"}`},
	{"not-json", "run-s", "ask.requested", `Which one?`},
	{"other-event", "run-s", "task.paused", `{"kind":"confirm"}`},
}

var asksAsTheyShouldBeRead = map[string]string{
	"confirm":          `{"kind":"ask_confirm","message":"Proceed?","question":"Proceed?"}`,
	"single-choices":   `{"kind":"ask_input","question":"Which?","message":"Which?","selectionMode":"single","options":[{"key":"1","label":"A","value":"A"},{"key":"3","label":"B","value":"B"}]}`,
	"multiple-choices": `{"kind":"ask_input","question":"Which?","message":"Which?","selectionMode":"multiple","options":[{"key":"1","label":"A","value":"A"},{"key":"2","label":"B","value":"B"}]}`,
	"input":            `{"kind":"ask_input","message":"Name?","question":"Name?"}`,
	"input-choice":     `{"kind":"ask_input","question":"Pick","message":"Pick","selectionMode":"single","options":[{"key":"x","label":"X","value":"x"}]}`,
}

func TestTheAskShapeMigrationRewritesLegacyAsksAndLeavesTheRest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database := databaseUnderAnOrdinaryOwner(t, ctx, administratorDatabase(t, ctx))
	migrateBefore(t, ctx, database, askShapeMigrationName)
	if errorValue := database.Exec(ctx, `INSERT INTO task_run (task_run_id, current_agent_profile_name, status, prompt, created_at, updated_at) VALUES ('run-s', 'assistant', 'waiting_user_input', 'fixture', now(), now())`); errorValue != nil {
		t.Fatal(errorValue)
	}
	for index, event := range asksStoredInTheOldShapes {
		if errorValue := database.Exec(ctx, `INSERT INTO task_event (task_event_id, task_run_id, name, body, created_at) VALUES ($1, $2, $3, $4, $5)`, event.id, event.run, event.name, event.body, time.Now().Add(-time.Hour+time.Duration(index)*time.Second)); errorValue != nil {
			t.Fatal(errorValue)
		}
	}

	runner := MigrationRunner{MigrationDirectoryPath: filepath.Join("..", "..", "..", "migrations")}
	if errorValue := runner.ApplyMigrations(ctx, database); errorValue != nil {
		t.Fatalf("the migration did not apply over old asks: %v", errorValue)
	}
	migrated := storedEventsByID(t, ctx, database)

	for id, expected := range asksAsTheyShouldBeRead {
		if !sameJSON(t, migrated[id].body, expected) {
			t.Fatalf("%s is stored as %s, expected %s", id, migrated[id].body, expected)
		}
	}
	for _, untouched := range []string{"current-input", "current-confirm", "not-json", "other-event"} {
		if migrated[untouched].body != findAskFixture(untouched).body {
			t.Fatalf("%s was rewritten to %s", untouched, migrated[untouched].body)
		}
	}
	if errorValue := database.Exec(ctx, readMigration(t, askShapeMigrationName)); errorValue != nil || !sameStoredEvents(migrated, storedEventsByID(t, ctx, database)) {
		t.Fatalf("running the migration a second time changed rows or failed: %v", errorValue)
	}
}

func findAskFixture(id string) storedEvent {
	for _, event := range asksStoredInTheOldShapes {
		if event.id == id {
			return event
		}
	}
	return storedEvent{}
}

func sameJSON(t *testing.T, left string, right string) bool {
	t.Helper()
	var leftValue, rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
