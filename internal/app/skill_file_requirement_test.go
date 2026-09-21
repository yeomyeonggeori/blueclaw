package app

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/skill"
)

func skillsRootDeclaringAFont(t *testing.T, fontPaths []string) string {
	t.Helper()
	skillsRoot := filepath.Join(t.TempDir(), "skills")
	skillPath := filepath.Join(skillsRoot, "pdf")
	if errorValue := os.MkdirAll(skillPath, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	document := `---
name: pdf
description: Writes a PDF from a source.
metadata:
  kim.intern.requires-any-file: "` + strings.Join(fontPaths, " ") + `"
---
Write the PDF.
`
	if errorValue := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), []byte(document), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	return skillsRoot
}

func TestASkillWhoseDeclaredFileIsAbsentIsLeftOutOfThePrompt(t *testing.T) {
	absentFontPath := filepath.Join(t.TempDir(), "NanumGothic.ttf")

	discovered := readSkillInstructions(t.TempDir(), skillsRootDeclaringAFont(t, []string{absentFontPath}), []string{})

	if len(discovered.Selectable) != 0 {
		t.Fatalf("a skill that would write the wrong file must not be offered, got %+v", discovered.Selectable)
	}
	if len(discovered.Unavailable) != 1 || discovered.Unavailable[0].Name != "pdf" {
		t.Fatalf("expected pdf listed as unavailable, got %+v", discovered.Unavailable)
	}
	missingPaths := discovered.Unavailable[0].MissingAnyFilePaths
	if len(missingPaths) != 1 || missingPaths[0] != absentFontPath {
		t.Fatalf("expected the path that would have done, got %v", missingPaths)
	}
}

func TestASkillWhoseDeclaredFileIsThereStaysSelectable(t *testing.T) {
	fontDirectory := t.TempDir()
	presentFontPath := filepath.Join(fontDirectory, "NotoSansCJK-Regular.ttc")
	if errorValue := os.WriteFile(presentFontPath, []byte("font"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	absentFontPath := filepath.Join(fontDirectory, "NanumGothic.ttf")

	discovered := readSkillInstructions(t.TempDir(), skillsRootDeclaringAFont(t, []string{absentFontPath, presentFontPath}), []string{})

	if len(discovered.Unavailable) != 0 {
		t.Fatalf("one of the alternatives being there is enough, got %+v", discovered.Unavailable)
	}
	if len(discovered.Selectable) != 1 || discovered.Selectable[0].Name != "pdf" {
		t.Fatalf("expected pdf offered, got %+v", discovered.Selectable)
	}
}

// A skill left out for a missing tool used to be the only one said out loud.
// Every other reason reached the inventory and nothing else, so a host that had
// quietly dropped a skill looked the same as one that never shipped it.
func TestStartupSaysEveryReasonASkillWasLeftOut(t *testing.T) {
	logDocument := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&logDocument, nil))

	logSkillsThisHostCannotSatisfy(logger, []skill.UnavailableSkill{
		{Name: "internkim-api", Path: "/skills/internkim-api/SKILL.md", MissingEnvironmentVariables: []string{"INTERNKIM_TOKEN"}},
		{Name: "pdf", Path: "/skills/pdf/SKILL.md", MissingAnyFilePaths: []string{"/usr/share/fonts/truetype/nanum/NanumGothic.ttf"}},
		{Name: "website", Path: "/skills/website/SKILL.md", MissingToolNames: []string{"site_serve"}},
	})

	for _, expectedText := range []string{
		`skill.environment.missing`, `missingEnvironment=INTERNKIM_TOKEN`,
		`skill.file.missing`, `anyOfTheseWouldHaveDone=/usr/share/fonts/truetype/nanum/NanumGothic.ttf`,
		`skill.tools.missing`, `missingTools=site_serve`,
	} {
		if !strings.Contains(logDocument.String(), expectedText) {
			t.Fatalf("startup must say %q; it said:\n%s", expectedText, logDocument.String())
		}
	}
}
