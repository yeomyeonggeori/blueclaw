//go:build !nobundledharness

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/skill"
)

func TestAScenarioSkillTellsTheModelWhereItsCopyLives(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "deckmaker")
	if errorValue := os.MkdirAll(sourcePath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	document := "---\nname: deckmaker\ndescription: Builds decks.\n---\n\nRun `<skill>/scripts/build` to build a deck.\n"
	if errorValue := os.WriteFile(filepath.Join(sourcePath, "SKILL.md"), []byte(document), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	workspacePath := t.TempDir()

	instructions, errorValue := loadVirtualSkillInstructions(VirtualSessionScenario{SkillDirectoryPaths: []string{sourcePath}}, workspacePath)
	if errorValue != nil {
		t.Fatalf("expected the skill to load: %v", errorValue)
	}

	if len(instructions) != 1 || strings.Contains(instructions[0].Prompt, skill.SkillDirectoryPlaceholder) {
		t.Fatalf("the model cannot resolve the placeholder itself, got %+v", instructions)
	}
	copiedPath := filepath.Join(workspacePath, "skills", "deckmaker")
	if !strings.Contains(instructions[0].Prompt, copiedPath+"/scripts/build") {
		t.Fatalf("expected the run's own copy of the skill, got %q", instructions[0].Prompt)
	}
}
