package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const (
	bluecollarModulePath  = "github.com/yeomyeonggeori/bluecollar/"
	defaultHarnessPackage = "internal/defaultharness"
	noBundledHarnessTag   = "!nobundledharness"
)

type sourceFile struct {
	path               string
	importsBluecollar  bool
	importsIntake      bool
	importsModel       bool
	isTest             bool
	hasNoBundledTag    bool
	isInDefaultHarness bool
}

func TestOnlyTheDefaultHarnessImportsBluecollar(t *testing.T) {
	for _, file := range listSourceFiles(t) {
		if file.importsBluecollar && !file.isTest && !file.isInDefaultHarness {
			t.Errorf("%s imports bluecollar; blueclaw is a meta-harness and only %s may name the default harness", file.path, defaultHarnessPackage)
		}
	}
}

func TestATestOutsideTheDefaultHarnessThatImportsBluecollarIsLeftOutOfTheNoBundledBuild(t *testing.T) {
	for _, file := range listSourceFiles(t) {
		if file.importsBluecollar && file.isTest && !file.isInDefaultHarness && !file.hasNoBundledTag {
			t.Errorf("%s imports bluecollar without //go:build %s, so the -tags nobundledharness build cannot compile without the submodule", file.path, noBundledHarnessTag)
		}
	}
}

func TestEveryDefaultHarnessFileIsLeftOutOfTheNoBundledBuild(t *testing.T) {
	for _, file := range listSourceFiles(t) {
		if file.isInDefaultHarness && !file.hasNoBundledTag {
			t.Errorf("%s is part of the default harness and lacks //go:build %s", file.path, noBundledHarnessTag)
		}
	}
}

func TestTheDefaultHarnessStillImportsBluecollar(t *testing.T) {
	for _, file := range listSourceFiles(t) {
		if file.isInDefaultHarness && file.importsBluecollar {
			return
		}
	}
	t.Errorf("%s no longer imports bluecollar; delete the package and its tags", defaultHarnessPackage)
}

func TestNothingImportsBluecollarIntake(t *testing.T) {
	for _, file := range listSourceFiles(t) {
		if file.importsIntake {
			t.Errorf("%s imports bluecollar intake; the agent plans its own turn and the host hands it facts (#543)", file.path)
		}
	}
}

func listSourceFiles(t *testing.T) []sourceFile {
	t.Helper()
	root := "../.."
	files := []sourceFile{}
	errorValue := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if entry.IsDir() {
			return skipDirectory(entry.Name())
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, describeSourceFile(t, root, path))
		}
		return nil
	})
	if errorValue != nil {
		t.Fatalf("walking the module failed: %v", errorValue)
	}
	return files
}

func skipDirectory(name string) error {
	switch name {
	case ".dependency", ".git", "node_modules", "testdata":
		return filepath.SkipDir
	}
	return nil
}

func describeSourceFile(t *testing.T, root string, path string) sourceFile {
	t.Helper()
	file, parseError := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly|parser.ParseComments)
	if parseError != nil {
		t.Fatalf("parsing %s failed: %v", path, parseError)
	}
	relative, _ := filepath.Rel(root, path)
	relative = filepath.ToSlash(relative)
	described := sourceFile{
		path:               relative,
		isTest:             strings.HasSuffix(path, "_test.go"),
		isInDefaultHarness: strings.HasPrefix(relative, defaultHarnessPackage+"/"),
		hasNoBundledTag:    constrainedAwayFromNoBundledBuild(file),
	}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		described.importsBluecollar = described.importsBluecollar || strings.HasPrefix(importPath, bluecollarModulePath)
		described.importsModel = described.importsModel || importPath == "github.com/yeomyeonggeori/blueprotocol/model"
		described.importsIntake = described.importsIntake || importPath == bluecollarModulePath+"intake"
	}
	return described
}

func constrainedAwayFromNoBundledBuild(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:build") && strings.Contains(comment.Text, noBundledHarnessTag) {
				return true
			}
		}
	}
	return false
}
