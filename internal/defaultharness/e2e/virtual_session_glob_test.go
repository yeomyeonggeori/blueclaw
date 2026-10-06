//go:build !nobundledharness

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnyDepthWorkspaceGlobFindsTheFileWhereverTheTaskDirectoryIs(t *testing.T) {
	workspacePath := t.TempDir()
	for _, relativePath := range []string{"artifacts/deck/build/review/contact-sheet-01.png", "private/people/person-1/artifacts/other/build/review/contact-sheet-01.png", "artifacts/deck/build/review/notes.txt"} {
		filePath := filepath.Join(workspacePath, relativePath)
		if errorValue := os.MkdirAll(filepath.Dir(filePath), 0o755); errorValue != nil {
			t.Fatal(errorValue)
		}
		if errorValue := os.WriteFile(filePath, []byte("x"), 0o644); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	matches, errorValue := globWorkspace(workspacePath, "**/artifacts/*/build/review/contact-sheet-*.png")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(matches) != 2 {
		t.Fatalf("expected the contact sheet at both depths, got %v", matches)
	}
}
