package architecture

import (
	"os"
	"strings"
	"testing"
)

func TestTheNoBundledModuleFilesDifferOnlyByTheBluecollarLines(t *testing.T) {
	for _, pair := range [][2]string{{"go.mod", "go.nobundled.mod"}, {"go.sum", "go.nobundled.sum"}} {
		bundled := withoutBluecollarLines(t, "../../"+pair[0])
		unbundled := withoutBluecollarLines(t, "../../"+pair[1])
		if strings.Join(bundled, "\n") != strings.Join(unbundled, "\n") {
			t.Errorf("%s and %s differ beyond the bluecollar lines; regenerate %s from %s", pair[0], pair[1], pair[1], pair[0])
		}
	}
}

func TestTheNoBundledModuleFileNamesNoBluecollarLine(t *testing.T) {
	for _, line := range readLines(t, "../../go.nobundled.mod") {
		if strings.Contains(line, "yeomyeonggeori/bluecollar") {
			t.Errorf("go.nobundled.mod still names bluecollar: %q", line)
		}
	}
}

func withoutBluecollarLines(t *testing.T, path string) []string {
	t.Helper()
	kept := []string{}
	for _, line := range readLines(t, path) {
		if !strings.Contains(line, "yeomyeonggeori/bluecollar") {
			kept = append(kept, line)
		}
	}
	return kept
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatalf("reading %s: %v", path, errorValue)
	}
	return strings.Split(string(content), "\n")
}
