package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/config"
)

// One delivered skills root serves every instruction root, so a skill was read
// once per root and its whole body reached the prompt that many times.
func TestLoadAgentInstructionBundleCountsADeliveredSkillOnce(t *testing.T) {
	deliveredSkillsPath := t.TempDir()
	skillDirectoryPath := filepath.Join(deliveredSkillsPath, "presentation")
	if errorValue := os.MkdirAll(skillDirectoryPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	skillDocument := "---\nname: presentation\ndescription: Build a deck and export it.\n---\nPresentation body.\n"
	if errorValue := os.WriteFile(filepath.Join(skillDirectoryPath, "SKILL.md"), []byte(skillDocument), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Setenv("BLUECLAW_BUNDLED_SKILLS_PATH", deliveredSkillsPath)
	runtimeConfiguration := config.RuntimeConfiguration{}
	runtimeConfiguration.Terminal.WorkspaceRootPath = t.TempDir()

	instructionBundle := loadAgentInstructionBundle(runtimeConfiguration)

	deliveredCount := 0
	for _, skillInstruction := range instructionBundle.Skills {
		if skillInstruction.Name == "presentation" {
			deliveredCount++
		}
	}
	if deliveredCount != 1 {
		t.Fatalf("expected the delivered skill once, got %d", deliveredCount)
	}
}
