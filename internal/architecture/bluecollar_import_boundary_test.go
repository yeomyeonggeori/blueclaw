package architecture

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"
)

const (
	blueclawModulePath   = "github.com/yeomyeonggeori/blueclaw/"
	bluecollarModulePath = "github.com/yeomyeonggeori/bluecollar/"
)

var judgmentPackages = []string{"intake", "loop", "approval", "claimcheck", "visualcheck", "acpagent"}

var defaultHarnessWiringPackages = []string{
	"internal/app",
	"internal/bluecollaracp",
	"internal/e2e",
}

type listedPackage struct {
	ImportPath string
	Imports    []string
}

func TestOnlyDefaultHarnessWiringImportsBluecollarJudgment(t *testing.T) {
	importedJudgmentByPackage := judgmentImportsByPackage(t)
	permitted := toSet(defaultHarnessWiringPackages)

	for _, packagePath := range sortedKeys(importedJudgmentByPackage) {
		if !permitted[packagePath] {
			t.Errorf("%s imports bluecollar judgment %v; blueclaw is a meta-harness and only default-harness wiring may do that (#537)", packagePath, importedJudgmentByPackage[packagePath])
		}
	}
}

func TestEveryPermittedImporterStillImportsBluecollarJudgment(t *testing.T) {
	importedJudgmentByPackage := judgmentImportsByPackage(t)

	for _, packagePath := range defaultHarnessWiringPackages {
		if len(importedJudgmentByPackage[packagePath]) == 0 {
			t.Errorf("%s no longer imports bluecollar judgment; remove it from defaultHarnessWiringPackages", packagePath)
		}
	}
}

func TestNothingImportsBluecollarIntake(t *testing.T) {
	for packagePath, imported := range judgmentImportsByPackage(t) {
		if slices.Contains(imported, "intake") {
			t.Errorf("%s imports bluecollar intake; the agent plans its own turn and the host hands it facts (#543)", packagePath)
		}
	}
}

func judgmentImportsByPackage(t *testing.T) map[string][]string {
	t.Helper()
	judgmentImportPaths := toSet(prefixAll(bluecollarModulePath, judgmentPackages))
	importedJudgmentByPackage := map[string][]string{}
	for _, listed := range listProductionPackages(t) {
		for _, importPath := range listed.Imports {
			if judgmentImportPaths[importPath] {
				packagePath := strings.TrimPrefix(listed.ImportPath, strings.TrimSuffix(blueclawModulePath, "/"))
				packagePath = strings.TrimPrefix(packagePath, "/")
				importedJudgmentByPackage[packagePath] = append(importedJudgmentByPackage[packagePath], strings.TrimPrefix(importPath, bluecollarModulePath))
			}
		}
	}
	return importedJudgmentByPackage
}

func listProductionPackages(t *testing.T) []listedPackage {
	t.Helper()
	command := exec.Command("go", "list", "-json", "./...")
	command.Dir = "../.."
	var output bytes.Buffer
	command.Stdout = &output
	if errorValue := command.Run(); errorValue != nil {
		t.Fatalf("go list failed: %v", errorValue)
	}
	decoder := json.NewDecoder(&output)
	listed := []listedPackage{}
	for decoder.More() {
		var entry listedPackage
		if errorValue := decoder.Decode(&entry); errorValue != nil {
			t.Fatalf("go list output did not decode: %v", errorValue)
		}
		listed = append(listed, entry)
	}
	return listed
}

func prefixAll(prefix string, values []string) []string {
	prefixed := make([]string, len(values))
	for index, value := range values {
		prefixed[index] = prefix + value
	}
	return prefixed
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func sortedKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
