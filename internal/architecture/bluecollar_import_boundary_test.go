package architecture

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
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

var planningVocabulary = []string{"TurnDecision", "IntakeDecisionRequest", "IntakeDecisions", "IntakeCallLedger"}

func TestOnlyDefaultHarnessWiringNamesBluecollarPlanningTypes(t *testing.T) {
	permitted := toSet(defaultHarnessWiringPackages)
	agentcontractPath := bluecollarModulePath + "agentcontract"
	for _, use := range planningVocabularyUses(t, agentcontractPath) {
		if !permitted[use.packagePath] {
			t.Errorf("%s names agentcontract.%s at %s; the host hands the agent facts and never a decision (#543)", use.packagePath, use.name, use.position)
		}
	}
}

type vocabularyUse struct {
	packagePath string
	name        string
	position    string
}

func planningVocabularyUses(t *testing.T, agentcontractPath string) []vocabularyUse {
	t.Helper()
	root := "../.."
	uses := []vocabularyUse{}
	errorValue := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if entry.IsDir() {
			if name := entry.Name(); name == ".dependency" || name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, parseError := parser.ParseFile(fileSet, path, nil, 0)
		if parseError != nil {
			return parseError
		}
		packagePath, _ := filepath.Rel(root, filepath.Dir(path))
		uses = append(uses, vocabularyUsesIn(fileSet, file, filepath.ToSlash(packagePath), agentcontractPath)...)
		return nil
	})
	if errorValue != nil {
		t.Fatalf("walking the module failed: %v", errorValue)
	}
	return uses
}

func vocabularyUsesIn(fileSet *token.FileSet, file *ast.File, packagePath string, agentcontractPath string) []vocabularyUse {
	localName := ""
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != agentcontractPath {
			continue
		}
		localName = "agentcontract"
		if spec.Name != nil {
			localName = spec.Name.Name
		}
	}
	uses := []vocabularyUse{}
	if localName == "" {
		return uses
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, isSelector := node.(*ast.SelectorExpr)
		if !isSelector {
			return true
		}
		qualifier, isIdentifier := selector.X.(*ast.Ident)
		if isIdentifier && qualifier.Name == localName && slices.Contains(planningVocabulary, selector.Sel.Name) {
			uses = append(uses, vocabularyUse{packagePath: packagePath, name: selector.Sel.Name, position: fileSet.Position(selector.Pos()).String()})
		}
		return true
	})
	return uses
}
