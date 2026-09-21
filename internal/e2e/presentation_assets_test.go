//go:build appliance

// These checks read the appliance presentation skill bundle directly.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationAssetsStayPureSkill(t *testing.T) {
	skillDirectoryPath := presentationSkillPath()
	if skillDirectoryPath == "" {
		t.Skip("root presentation skill is unavailable")
	}

	assertMissingPath(t, filepath.Join(skillDirectoryPath, "scripts", "create_deck.py"))
	assertMissingPath(t, filepath.Join(skillDirectoryPath, "assets", "template.md"))
	assertMissingPath(t, filepath.Join(skillDirectoryPath, "assets", "design.md"))
	assertMissingPath(t, filepath.Join(skillDirectoryPath, "references"))
	assertPresentPath(t, filepath.Join(skillDirectoryPath, "assets", "layouts.md"))
	assertPresentPath(t, filepath.Join(skillDirectoryPath, "assets", "minimal-design.md"))
	assertPresentPath(t, filepath.Join(skillDirectoryPath, "assets", "webfonts.md"))

	skillDocument := readTestFile(t, filepath.Join(skillDirectoryPath, "SKILL.md"))
	if strings.Contains(skillDocument, "create_deck.py") {
		t.Fatal("SKILL.md should not mention create_deck.py")
	}
	if strings.Contains(skillDocument, "template.md") || strings.Contains(skillDocument, "references/") {
		t.Fatal("SKILL.md should not reference removed template/reference assets")
	}
	if !strings.Contains(skillDocument, "deck-brief.md") {
		t.Fatal("SKILL.md should require the current deck brief")
	}
	if strings.Contains(skillDocument, "assets/design.md") {
		t.Fatal("SKILL.md should not reference a separate design template asset")
	}
	if !strings.Contains(skillDocument, "assets/layouts.md") {
		t.Fatal("SKILL.md should reference layout guidance")
	}
	if !strings.Contains(skillDocument, "assets/minimal-design.md") {
		t.Fatal("SKILL.md should reference minimal design guidance")
	}
	if !strings.Contains(skillDocument, "slides.html` is the source of truth") {
		t.Fatal("SKILL.md should make slides.html the source of truth")
	}
	if !strings.Contains(skillDocument, "<skill>/scripts/build.sh") {
		t.Fatal("SKILL.md should guide agents to run the bundled build script from wherever the host installed the skill")
	}
	if !strings.Contains(skillDocument, "With no `FORMATS`") {
		t.Fatal("SKILL.md should explain html-only builds")
	}

	assertMissingPath(t, filepath.Join(skillDirectoryPath, "assets", "build.sh"))
	assertPresentPath(t, filepath.Join(skillDirectoryPath, "scripts", "build.sh"))

	buildScript := readTestFile(t, filepath.Join(skillDirectoryPath, "scripts", "build.sh"))
	if !strings.Contains(buildScript, "FORMATS=") || !strings.Contains(buildScript, "Building requested formats") {
		t.Fatal("build.sh should support requested format narrowing")
	}
	if !strings.Contains(buildScript, "DESIGN.md missing") {
		t.Fatal("build.sh should call out a missing DESIGN.md")
	}
	if !strings.Contains(buildScript, "design-source: DESIGN.md") {
		t.Fatal("build.sh should require slides.html to reference DESIGN.md")
	}
	if strings.Contains(buildScript, "BUILD_SRC") || strings.Contains(buildScript, "with-design") {
		t.Fatal("build.sh should not treat DESIGN.md as a late theme compiler")
	}
	if !strings.Contains(buildScript, "HTML-first source only") {
		t.Fatal("build.sh should reject non-HTML source files")
	}
	if !strings.Contains(buildScript, "slides.html must contain slide <section> elements") {
		t.Fatal("build.sh should require slide sections")
	}
	if !strings.Contains(buildScript, `rm -f "${BUILD_PATH}/${NAME}.html"`) {
		t.Fatal("build.sh should clear previous outputs before rebuilding")
	}
	if !strings.Contains(buildScript, `HTML_EXPORT_SCRIPT`) {
		t.Fatal("build.sh should use the HTML-first exporter")
	}
	if !strings.Contains(buildScript, `export HOME="${TMPDIR}/home"`) {
		t.Fatal("build.sh should keep home-scoped temporary files in the private build temp directory")
	}
	if strings.Contains(buildScript, "command -v marp") {
		t.Fatal("build.sh should not use ambiguous global Marp from the caller environment")
	}
}

func TestPresentationSkillCarriesDesignGuidanceWithoutTemplateAsset(t *testing.T) {
	skillDirectoryPath := presentationSkillPath()
	if skillDirectoryPath == "" {
		t.Skip("root presentation skill is unavailable")
	}

	skillDocument := readTestFile(t, filepath.Join(skillDirectoryPath, "SKILL.md"))
	for _, fragment := range []string{"`colors`", "`typography`", "`layout`", "Paperlogy", "Noto Sans KR", "system-ui"} {
		if !strings.Contains(skillDocument, fragment) {
			t.Fatalf("expected SKILL.md to preserve design guidance fragment %q", fragment)
		}
	}
	if !strings.Contains(skillDocument, "`DESIGN.md` the design brief") {
		t.Fatal("SKILL.md should define DESIGN.md as an authoring brief")
	}
	if !strings.Contains(skillDocument, "shell heredocs") {
		t.Fatal("SKILL.md should keep source authoring on write")
	}
	if !strings.Contains(skillDocument, "Avoid bullet-only decks") {
		t.Fatal("SKILL.md should discourage bullet-only decks")
	}
	if !strings.Contains(skillDocument, "HTML is the layout surface") {
		t.Fatal("SKILL.md should guide agents toward HTML layout authoring")
	}
	layoutDocument := readTestFile(t, filepath.Join(skillDirectoryPath, "assets", "layouts.md"))
	for _, fragment := range []string{"Cards", "Comparison", "Matrix", "Evidence Stack", "Consulting Page", ".cards", ".comparison", ".frame"} {
		if !strings.Contains(layoutDocument, fragment) {
			t.Fatalf("expected layouts.md to include layout fragment %q", fragment)
		}
	}
	minimalDesignDocument := readTestFile(t, filepath.Join(skillDirectoryPath, "assets", "minimal-design.md"))
	for _, fragment := range []string{"colors:", "typography:", "layout:", "Black-and-white", "design-source: DESIGN.md"} {
		if !strings.Contains(minimalDesignDocument, fragment) {
			t.Fatalf("expected minimal-design.md to include design fragment %q", fragment)
		}
	}
	webfontDocument := readTestFile(t, filepath.Join(skillDirectoryPath, "assets", "webfonts.md"))
	for _, fragment := range []string{"Paperlogy", "Freesentation", "Pretendard", "Noto Sans KR", "@import", "font-family"} {
		if !strings.Contains(webfontDocument, fragment) {
			t.Fatalf("expected webfonts.md to include webfont fragment %q", fragment)
		}
	}
}

func assertMissingPath(t *testing.T, path string) {
	t.Helper()
	if _, errorValue := os.Stat(path); !os.IsNotExist(errorValue) {
		t.Fatalf("expected %s to be removed", path)
	}
}

func assertPresentPath(t *testing.T, path string) {
	t.Helper()
	if _, errorValue := os.Stat(path); errorValue != nil {
		t.Fatalf("expected %s to exist: %v", path, errorValue)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(content)
}
