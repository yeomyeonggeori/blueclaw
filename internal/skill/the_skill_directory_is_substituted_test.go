package skill

import (
	"strings"
	"testing"
)

func TestBuildSkillPromptResolvesTheSkillDirectory(t *testing.T) {
	prompt := (SkillPromptBuilder{}).BuildSkillPrompt([]SkillBundle{{
		Name:          "presentation",
		Instruction:   "Run `python3 <skill>/scripts/build.py` from <skill>.",
		DirectoryPath: "/opt/blueclaw/skills/presentation",
	}})

	if strings.Contains(prompt, SkillDirectoryPlaceholder) {
		t.Fatalf("the model reads the prompt and cannot resolve the placeholder itself: %q", prompt)
	}
	if !strings.Contains(prompt, "/opt/blueclaw/skills/presentation/scripts/build.py") {
		t.Fatalf("expected the skill's own directory, got %q", prompt)
	}
}

func TestBuildSkillPromptLeavesThePlaceholderWhenNoDirectoryIsKnown(t *testing.T) {
	prompt := (SkillPromptBuilder{}).BuildSkillPrompt([]SkillBundle{{
		Name:        "presentation",
		Instruction: "Run `python3 <skill>/scripts/build.py`.",
	}})

	if !strings.Contains(prompt, SkillDirectoryPlaceholder) {
		t.Fatalf("a bundle with no directory has nothing to substitute, got %q", prompt)
	}
}
