package persona

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSoulHistoryAppendsWithExpectedVersionAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	first, errorValue := AppendSoulRevision(root, 1, []byte(`{"schemaVersion":1,"workingStyle":["steady"]}`), "sample improvement", []string{"task-1"}, "reflection")
	if errorValue != nil || first.Version != 2 {
		t.Fatalf("append failed: %+v %v", first, errorValue)
	}
	if _, errorValue := AppendSoulRevision(root, 1, []byte(`{"schemaVersion":1}`), "stale", nil, "reflection"); errorValue == nil {
		t.Fatal("expected stale revision rejection")
	}
	history, errorValue := ReadSoulHistory(root)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(history) != 2 || string(history[1].Document) == "" {
		t.Fatalf("unexpected history: %+v", history)
	}
	document, errorValue := ReadSoulDocument(root)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(document) != string(history[1].Document) {
		t.Fatal("current document differs from ledger")
	}
	projection, errorValue := os.ReadFile(filepath.Join(root, SoulFileName))
	var projectedDocument, ledgerDocument any
	if errorValue := json.Unmarshal(projection, &projectedDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := json.Unmarshal(history[1].Document, &ledgerDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue != nil || !jsonEqual(projectedDocument, ledgerDocument) {
		t.Fatalf("soul projection differs from ledger: %s %v", projection, errorValue)
	}
}

func jsonEqual(left, right any) bool {
	leftDocument, leftError := json.Marshal(left)
	rightDocument, rightError := json.Marshal(right)
	return leftError == nil && rightError == nil && string(leftDocument) == string(rightDocument)
}

func TestSoulHistoryBoundsRevisions(t *testing.T) {
	root := t.TempDir()
	for version := 1; version <= maximumSoulHistory; version++ {
		if _, errorValue := AppendSoulRevision(root, version, []byte(`{"schemaVersion":1}`), "sample", nil, "reflection"); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	history, errorValue := ReadSoulHistory(root)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(history) != maximumSoulHistory {
		t.Fatalf("expected bounded history, got %d", len(history))
	}
	if history[0].Version != 2 {
		t.Fatalf("expected oldest retained version 2, got %d", history[0].Version)
	}
}

func TestSoulHistoryPathIsPrivateWorkspaceState(t *testing.T) {
	if filepath.Base(soulHistoryPath("/workspace")) != "soul-history.json" {
		t.Fatal("unexpected soul history path")
	}
}

func TestSoulHistoryDoesNotFallbackAfterLedgerCorruption(t *testing.T) {
	root := t.TempDir()
	if errorValue := os.MkdirAll(filepath.Dir(soulHistoryPath(root)), 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(root, SoulFileName), []byte(`{"schemaVersion":1}`), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(soulHistoryPath(root), []byte("broken"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := ReadSoulDocument(root); errorValue == nil {
		t.Fatal("corrupt ledger must not silently fall back")
	}
}
