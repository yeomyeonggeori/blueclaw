package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	toolNamePattern = regexp.MustCompile(`Name:\s*(?:toolcontract\.)?([A-Za-z0-9_".]+)`)
	toolDescription = regexp.MustCompile(`Description:\s*"((?:[^"\\]|\\.)*)"`)
	toolDefinition  = regexp.MustCompile(`(?s)toolcontract\.ToolDefinition\{(.*?)\n\t*\}`)
)

type ToolEntry struct {
	Name             string
	Source           string
	DescriptionBytes int
}

func Tools(sourceRoot string) ([]ToolEntry, error) {
	entries := []ToolEntry{}
	errorValue := walkSourceFiles(sourceRoot, func(path string, source string) {
		for _, definition := range toolDefinition.FindAllStringSubmatch(source, -1) {
			name := toolNamePattern.FindStringSubmatch(definition[1])
			if name == nil {
				continue
			}
			descriptionBytes := 0
			if description := toolDescription.FindStringSubmatch(definition[1]); description != nil {
				descriptionBytes = len(description[1])
			}
			entries = append(entries, ToolEntry{
				Name:             toolCatalogName(name[1]),
				Source:           filepath.Base(path),
				DescriptionBytes: descriptionBytes,
			})
		}
	})
	if errorValue != nil {
		return nil, errorValue
	}
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Name < entries[right].Name })
	return entries, nil
}

// A descriptor whose name is only known at runtime has no name to print, and printing
// the expression that produces it would read as a tool nobody can call.
func toolCatalogName(nameExpression string) string {
	if strings.HasPrefix(nameExpression, `"`) {
		return strings.Trim(nameExpression, `"`)
	}
	if strings.Contains(nameExpression, "toolDescriptor") {
		return "(named by the device catalog)"
	}
	return nameExpression
}

func walkSourceFiles(sourceRoot string, visit func(path string, source string)) error {
	return filepath.Walk(sourceRoot, func(path string, info os.FileInfo, walkError error) error {
		if walkError != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkError
		}
		source, readError := os.ReadFile(path)
		if readError != nil {
			return readError
		}
		visit(path, string(source))
		return nil
	})
}
