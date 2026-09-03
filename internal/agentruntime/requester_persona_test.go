package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/persona"
)

func TestRequesterPersonaInstructionReadsTheRequestersDocumentOnly(t *testing.T) {
	workspacePath := t.TempDir()
	documentPath, _ := persona.UserDocumentPath(workspacePath, "person-1")
	if errorValue := os.MkdirAll(filepath.Dir(documentPath), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(documentPath, []byte(`{"schemaVersion": 1, "callMe": "샘플님", "preferences": ["Give me the command first."]}`), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}

	instruction := requesterPersonaInstruction(workspacePath, "person-1")
	if !strings.Contains(instruction, "Call them 샘플님.") || !strings.Contains(instruction, "Give me the command first.") {
		t.Fatalf("expected the requester's document in the instruction, got %q", instruction)
	}
	if requesterPersonaInstruction(workspacePath, "person-2") != "" {
		t.Fatal("expected nothing for a person without a document")
	}
	if requesterPersonaInstruction(workspacePath, "../person-1") != "" {
		t.Fatal("expected a person ID that leaves the directory to be refused")
	}
}

func TestRequesterPersonaInstructionLeavesOutADocumentTheSchemaRefuses(t *testing.T) {
	workspacePath := t.TempDir()
	documentPath, _ := persona.UserDocumentPath(workspacePath, "person-1")
	if errorValue := os.MkdirAll(filepath.Dir(documentPath), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(documentPath, []byte(`{"schemaVersion": 1, "callMe": "샘플님", "nickname": "kim"}`), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}

	if instruction := requesterPersonaInstruction(workspacePath, "person-1"); instruction != "" {
		t.Fatalf("expected a refused document to render nothing, got %q", instruction)
	}
}
