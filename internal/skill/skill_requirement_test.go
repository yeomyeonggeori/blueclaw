package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func skillBundleFromDocument(t *testing.T, document string) SkillBundle {
	t.Helper()
	directoryPath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(directoryPath, "SKILL.md"), []byte(document), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	skillBundle, errorValue := (SkillLoader{}).LoadSkillBundle(directoryPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return skillBundle
}

func TestSkillLoaderReadsRequiredEnvironmentFromTheMetadataMap(t *testing.T) {
	skillBundle := skillBundleFromDocument(t, `---
name: internkim-api
description: Drives the company workspace over its public API.
compatibility: Requires python3 and a personal access token in INTERNKIM_TOKEN.
metadata:
  kim.intern.tool-references: "shell"
  kim.intern.requires-environment: "INTERNKIM_TOKEN"
---
Body.
`)

	required := skillBundle.RequiredEnvironmentVariables
	if len(required) != 1 || required[0] != "INTERNKIM_TOKEN" {
		t.Fatalf("the machine-readable requirement lives in the metadata map, got %+v", required)
	}
	if len(skillBundle.ReferencedToolNames()) != 1 {
		t.Fatalf("the neighbouring key must keep working, got %+v", skillBundle.ReferencedToolNames())
	}
}

func TestSkillLoaderReadsRequiredEnvironmentFromAList(t *testing.T) {
	skillBundle := skillBundleFromDocument(t, `---
name: two-variables
description: Wants two of them.
metadata:
  kim.intern.requires-environment:
    - FIRST_TOKEN
    - SECOND_TOKEN
---
Body.
`)

	required := skillBundle.RequiredEnvironmentVariables
	if len(required) != 2 || required[0] != "FIRST_TOKEN" || required[1] != "SECOND_TOKEN" {
		t.Fatalf("expected both variables, got %+v", required)
	}
}

// The compatibility line is prose for a reader. Nothing resolves a requirement
// out of it, so a skill that only writes prose requires nothing.
func TestProseAloneDeclaresNoRequirement(t *testing.T) {
	skillBundle := skillBundleFromDocument(t, `---
name: prose-only
description: Says what it needs in words.
compatibility: Requires a personal access token in INTERNKIM_TOKEN.
---
Body.
`)

	if len(skillBundle.RequiredEnvironmentVariables) != 0 {
		t.Fatalf("prose is not a requirement, got %+v", skillBundle.RequiredEnvironmentVariables)
	}
	if len(skillBundle.MissingEnvironmentVariables()) != 0 {
		t.Fatalf("a skill declaring nothing is available, got %+v", skillBundle.MissingEnvironmentVariables())
	}
}

func TestAnEmptyVariableCountsAsUnset(t *testing.T) {
	skillBundle := SkillBundle{RequiredEnvironmentVariables: []string{"INTERNKIM_TOKEN"}}
	t.Setenv("INTERNKIM_TOKEN", "   ")

	missingVariableNames := skillBundle.MissingEnvironmentVariables()

	if len(missingVariableNames) != 1 || missingVariableNames[0] != "INTERNKIM_TOKEN" {
		t.Fatalf("a variable set to blank is a variable the skill cannot use, got %v", missingVariableNames)
	}
}
