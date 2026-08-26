package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillLoaderParsesFrontmatterMetadata(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document := `---
name: presentation
description: Create presentation decks.
license: Apache-2.0
compatibility: Requires a POSIX shell.
metadata:
  author: InternKim
tool-references:
  - shell
  - file_write
---
# Simple Slides

Build slides.
`
	if errorValue := os.WriteFile(documentPath, []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if skillBundle.Name != "presentation" {
		t.Fatalf("expected name from frontmatter, got %q", skillBundle.Name)
	}
	if skillBundle.Description != "Create presentation decks." {
		t.Fatalf("expected description from frontmatter, got %q", skillBundle.Description)
	}
	if !containsToolReference(skillBundle.ToolReferences, "shell") || !containsToolReference(skillBundle.ToolReferences, "file_write") {
		t.Fatalf("expected tool references, got %+v", skillBundle.ToolReferences)
	}
	if skillBundle.Instruction != "# Simple Slides\n\nBuild slides." {
		t.Fatalf("expected frontmatter to be stripped, got %q", skillBundle.Instruction)
	}
}

func TestSkillLoaderFallsBackForLegacySkillDocument(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	if errorValue := os.WriteFile(documentPath, []byte("Use this legacy skill."), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if skillBundle.Name != filepath.Base(directoryPath) {
		t.Fatalf("expected directory name fallback, got %q", skillBundle.Name)
	}
	if skillBundle.Instruction != "Use this legacy skill." {
		t.Fatalf("expected legacy body as instruction, got %q", skillBundle.Instruction)
	}
	if skillBundle.Description != "Use this legacy skill." {
		t.Fatalf("expected first paragraph description fallback, got %q", skillBundle.Description)
	}
	if len(skillBundle.ToolReferences) != 0 {
		t.Fatalf("expected empty metadata fallback, got %+v", skillBundle)
	}
}

func TestSkillLoaderParsesSpaceSeparatedToolReferences(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document := `---
name: file-work
description: Work with files.
tool-references: file_read file_write
---
Use files.
`
	if errorValue := os.WriteFile(documentPath, []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if !containsToolReference(skillBundle.ToolReferences, "file_read") || !containsToolReference(skillBundle.ToolReferences, "file_write") {
		t.Fatalf("expected space separated tool references, got %+v", skillBundle.ToolReferences)
	}
}

func TestSkillLoaderDoesNotExposeLegacyAllowedTools(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document := `---
name: git-work
description: Work with git.
allowed-tools:
  - Bash(git *)
---
Use git.
`
	if errorValue := os.WriteFile(documentPath, []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if len(skillBundle.ToolReferences) != 0 {
		t.Fatalf("expected only tool-references to expose tools, got %+v", skillBundle.ToolReferences)
	}
}

func TestSkillLoaderIgnoresAllowedToolsWhenToolReferencesExist(t *testing.T) {
	directoryPath := t.TempDir()
	documentPath := filepath.Join(directoryPath, "SKILL.md")
	document := `---
name: file-work
description: Work with files.
tool-references: file_read file_write
allowed-tools: shell
---
Use files.
`
	if errorValue := os.WriteFile(documentPath, []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	expectedToolReferences := []ToolReference{"file_read", "file_write"}
	if len(skillBundle.ToolReferences) != len(expectedToolReferences) {
		t.Fatalf("expected unique tool references, got %+v", skillBundle.ToolReferences)
	}
	for index, expectedToolReference := range expectedToolReferences {
		if skillBundle.ToolReferences[index] != expectedToolReference {
			t.Fatalf("expected tool reference %q at %d, got %+v", expectedToolReference, index, skillBundle.ToolReferences)
		}
	}
}

func containsToolReference(values []ToolReference, target ToolReference) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestSkillLoaderReadsToolReferencesFromTheMetadataMap(t *testing.T) {
	directoryPath := t.TempDir()
	document := `---
name: calculator
description: Calculate an expression.
metadata:
  kim.intern.tool-references: "shell"
---
Use the evaluator.
`
	if errorValue := os.WriteFile(filepath.Join(directoryPath, "SKILL.md"), []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}

	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	toolNames := skillBundle.ReferencedToolNames()
	if len(toolNames) != 1 || toolNames[0] != "shell" {
		t.Fatalf("a skill that has to satisfy the Agent Skills schema declares its tools here, got %+v", toolNames)
	}
}
