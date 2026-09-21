package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillLoaderReadsRequiredFilesFromTheMetadataMap(t *testing.T) {
	skillBundle := skillBundleFromDocument(t, `---
name: pdf
description: Writes a PDF.
metadata:
  kim.intern.tool-references: "bash"
  kim.intern.requires-any-file: "/usr/share/fonts/truetype/nanum/NanumGothic.ttf /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
---
Body.
`)

	required := skillBundle.RequiredAnyFilePaths
	if len(required) != 2 || required[0] != "/usr/share/fonts/truetype/nanum/NanumGothic.ttf" {
		t.Fatalf("a skill names the files that would do in the metadata map, got %+v", required)
	}
	if len(skillBundle.ReferencedToolNames()) != 1 {
		t.Fatalf("the neighbouring key must keep working, got %+v", skillBundle.ReferencedToolNames())
	}
}

func TestSkillLoaderReadsRequiredFilesFromAList(t *testing.T) {
	skillBundle := skillBundleFromDocument(t, `---
name: two-files
description: Either would do.
metadata:
  kim.intern.requires-any-file:
    - /first/font.ttf
    - /second/font.ttc
---
Body.
`)

	required := skillBundle.RequiredAnyFilePaths
	if len(required) != 2 || required[0] != "/first/font.ttf" || required[1] != "/second/font.ttc" {
		t.Fatalf("expected both paths, got %+v", required)
	}
}

func TestOneOfTheDeclaredFilesBeingThereSatisfiesAllOfThem(t *testing.T) {
	directoryPath := t.TempDir()
	presentPath := filepath.Join(directoryPath, "present.ttf")
	if errorValue := os.WriteFile(presentPath, []byte("font"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	skillBundle := SkillBundle{RequiredAnyFilePaths: []string{filepath.Join(directoryPath, "absent.ttf"), presentPath}}

	if missing := skillBundle.MissingAnyFilePaths(); len(missing) != 0 {
		t.Fatalf("alternatives: one readable file is enough, got %v", missing)
	}
}

func TestNoneOfTheDeclaredFilesBeingThereReportsAllOfThem(t *testing.T) {
	directoryPath := t.TempDir()
	firstPath := filepath.Join(directoryPath, "first.ttf")
	secondPath := filepath.Join(directoryPath, "second.ttc")
	skillBundle := SkillBundle{RequiredAnyFilePaths: []string{firstPath, secondPath}}

	missing := skillBundle.MissingAnyFilePaths()

	if len(missing) != 2 || missing[0] != firstPath || missing[1] != secondPath {
		t.Fatalf("the reader needs every path that would have done, got %v", missing)
	}
}

func TestADirectoryAtTheDeclaredPathIsNotTheFile(t *testing.T) {
	directoryPath := t.TempDir()
	if errorValue := os.Mkdir(filepath.Join(directoryPath, "NanumGothic.ttf"), 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	skillBundle := SkillBundle{RequiredAnyFilePaths: []string{filepath.Join(directoryPath, "NanumGothic.ttf")}}

	if missing := skillBundle.MissingAnyFilePaths(); len(missing) != 1 {
		t.Fatalf("a directory named like a font is not a font, got %v", missing)
	}
}

// Absence is the normal state of a variable nobody was told to set, and the
// normal state of a font is being installed. A skill declaring neither is
// available, which is what every deployment that has never heard of this key
// keeps being.
func TestASkillDeclaringNoFileIsAvailable(t *testing.T) {
	skillBundle := skillBundleFromDocument(t, `---
name: declares-nothing
description: Needs nothing of the machine.
---
Body.
`)

	if len(skillBundle.RequiredAnyFilePaths) != 0 {
		t.Fatalf("expected no declaration, got %+v", skillBundle.RequiredAnyFilePaths)
	}
	if missing := skillBundle.MissingAnyFilePaths(); len(missing) != 0 {
		t.Fatalf("a skill declaring nothing is available, got %v", missing)
	}
}
