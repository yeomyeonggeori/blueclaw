package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPackageThatDefinesToolsExplainsWhatTheModelSees(t *testing.T) {
	packagesDefiningTools := map[string]bool{}
	internalRoot := filepath.Join("..", "..", "internal")
	walkError := filepath.Walk(internalRoot, func(path string, info os.FileInfo, walkError error) error {
		if walkError != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkError
		}
		source, readError := os.ReadFile(path)
		if readError != nil {
			return readError
		}
		if strings.Contains(string(source), "toolcontract.ToolDefinition{") {
			packagesDefiningTools[filepath.Dir(path)] = true
		}
		return nil
	})
	if walkError != nil {
		t.Fatalf("walking internal packages failed: %v", walkError)
	}
	if len(packagesDefiningTools) == 0 {
		t.Fatal("no package defines a tool, which means this guard stopped finding the thing it guards")
	}

	for packagePath := range packagesDefiningTools {
		readme, readError := os.ReadFile(filepath.Join(packagePath, "README.md"))
		if readError != nil {
			t.Fatalf("%s registers tools the model reads and has no README: what it puts in front of the model is charged on every step forever and is invisible in a diff that reads as documentation", packagePath)
		}
		for _, section := range []string{"## Model Experience", "## Known Limitations and Deferred Work"} {
			if !strings.Contains(string(readme), section) {
				t.Fatalf("%s/README.md has no %q section", packagePath, section)
			}
		}
	}
}
