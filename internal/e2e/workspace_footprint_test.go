package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkspaceFile(t *testing.T, workspacePath string, relativePath string, content string) {
	t.Helper()
	fullPath := filepath.Join(workspacePath, filepath.FromSlash(relativePath))
	if errorValue := os.MkdirAll(filepath.Dir(fullPath), 0o700); errorValue != nil {
		t.Fatalf("preparing %s failed: %v", relativePath, errorValue)
	}
	if errorValue := os.WriteFile(fullPath, []byte(content), 0o600); errorValue != nil {
		t.Fatalf("writing %s failed: %v", relativePath, errorValue)
	}
}

func TestOnlyFilesThatWereAlreadyThereAreHeldToBeingUntouched(t *testing.T) {
	workspacePath := t.TempDir()
	writeWorkspaceFile(t, workspacePath, "skills/calendar/SKILL.md", "how to keep a calendar")
	writeWorkspaceFile(t, workspacePath, "shared/manifest.json", `{"entries":[]}`)
	writeWorkspaceFile(t, workspacePath, "private/people/person-1/notes.md", "notes")

	before, errorValue := workspaceFileDigests(workspacePath)
	if errorValue != nil {
		t.Fatalf("reading the workspace failed: %v", errorValue)
	}
	writeWorkspaceFile(t, workspacePath, "skills/calendar/SKILL.md", "how to keep a calendar, edited")
	writeWorkspaceFile(t, workspacePath, "private/people/person-1/report.pdf", "the deliverable")
	if errorValue := os.Remove(filepath.Join(workspacePath, "shared", "manifest.json")); errorValue != nil {
		t.Fatalf("removing the manifest failed: %v", errorValue)
	}

	after, errorValue := workspaceFileDigests(workspacePath)
	if errorValue != nil {
		t.Fatalf("reading the workspace failed: %v", errorValue)
	}
	changes := changedFilesOutsideWritableScope(before, after, nil)

	if len(changes) != 2 {
		t.Fatalf("a rewritten fixture and a deleted manifest are both the scenario touching what nobody asked about: %v", changes)
	}
	if !strings.Contains(strings.Join(changes, "; "), "skills/calendar/SKILL.md was rewritten") {
		t.Fatalf("expected the rewritten skill to be named: %v", changes)
	}
	if !strings.Contains(strings.Join(changes, "; "), "shared/manifest.json was deleted") {
		t.Fatalf("expected the deleted manifest to be named: %v", changes)
	}
	for _, change := range changes {
		if strings.Contains(change, "report.pdf") {
			t.Fatalf("a file the scenario created is its work, not a footprint to answer for: %v", changes)
		}
	}
}

func TestADeclaredWritablePathCoversWhatIsUnderIt(t *testing.T) {
	workspacePath := t.TempDir()
	writeWorkspaceFile(t, workspacePath, "shared/cache/dependencies/lock.json", "old")
	writeWorkspaceFile(t, workspacePath, "shared/public/index.html", "old")

	before, _ := workspaceFileDigests(workspacePath)
	writeWorkspaceFile(t, workspacePath, "shared/cache/dependencies/lock.json", "new")
	writeWorkspaceFile(t, workspacePath, "shared/public/index.html", "new")
	after, _ := workspaceFileDigests(workspacePath)

	changes := changedFilesOutsideWritableScope(before, after, []string{"shared/cache"})

	if len(changes) != 1 || !strings.Contains(changes[0], "shared/public/index.html") {
		t.Fatalf("a declared path covers everything under it and nothing beside it: %v", changes)
	}
}
