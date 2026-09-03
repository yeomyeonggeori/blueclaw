package persona

import (
	"os"
	"path/filepath"
	"testing"
)

const validUserDocument = `{"schemaVersion": 1, "callMe": "동혁"}`

func TestParseWithBackupSavesTheLastValidDocument(t *testing.T) {
	backupPath := filepath.Join(t.TempDir(), "state", "persona-backup", "user.json")
	user, document, isRestored, errorValue := ParseWithBackup(ParseUser, []byte(validUserDocument), backupPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if isRestored {
		t.Fatal("a valid document must not report a restore")
	}
	if user.CallMe != "동혁" || string(document) != validUserDocument {
		t.Fatalf("unexpected parse result: %+v %q", user, document)
	}
	saved, errorValue := os.ReadFile(backupPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(saved) != validUserDocument {
		t.Fatalf("the backup holds %q", saved)
	}
}

func TestParseWithBackupRestoresWhenTheDocumentIsBroken(t *testing.T) {
	backupPath := filepath.Join(t.TempDir(), "user.json")
	if _, _, _, errorValue := ParseWithBackup(ParseUser, []byte(validUserDocument), backupPath); errorValue != nil {
		t.Fatal(errorValue)
	}
	user, document, isRestored, errorValue := ParseWithBackup(ParseUser, []byte(`{"callMe": broken`), backupPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !isRestored {
		t.Fatal("a broken document with a valid backup must restore")
	}
	if user.CallMe != "동혁" || string(document) != validUserDocument {
		t.Fatalf("unexpected restore result: %+v %q", user, document)
	}
}

func TestParseWithBackupFailsWhenNothingIsValid(t *testing.T) {
	backupPath := filepath.Join(t.TempDir(), "user.json")
	broken := []byte(`{"callMe": broken`)
	_, document, isRestored, errorValue := ParseWithBackup(ParseUser, broken, backupPath)
	if errorValue == nil {
		t.Fatal("a broken document without a backup must fail")
	}
	if isRestored {
		t.Fatal("nothing valid exists to restore from")
	}
	if string(document) != string(broken) {
		t.Fatalf("the broken document must come back for diagnostics, got %q", document)
	}
}
