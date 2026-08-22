package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	taskEventPattern = regexp.MustCompile(`AppendTaskEvent\([^,]+,\s*"([a-z][a-zA-Z0-9_.]*)"`)
	toolNamePattern  = regexp.MustCompile(`Name:\s*(?:toolcontract\.)?([A-Za-z0-9_".]+)`)
	toolDescription  = regexp.MustCompile(`Description:\s*"((?:[^"\\]|\\.)*)"`)
	toolDefinition   = regexp.MustCompile(`(?s)toolcontract\.ToolDefinition\{(.*?)\n\t*\}`)
)

type TaskEventEntry struct {
	Name    string
	Sources []string
}

type ToolEntry struct {
	Name             string
	Source           string
	DescriptionBytes int
}

func TaskEvents(sourceRoot string) ([]TaskEventEntry, error) {
	sourcesByName := map[string]map[string]bool{}
	errorValue := walkSourceFiles(sourceRoot, func(path string, source string) {
		for _, match := range taskEventPattern.FindAllStringSubmatch(source, -1) {
			name := match[1]
			if sourcesByName[name] == nil {
				sourcesByName[name] = map[string]bool{}
			}
			sourcesByName[name][filepath.Base(filepath.Dir(path))] = true
		}
	})
	if errorValue != nil {
		return nil, errorValue
	}
	entries := make([]TaskEventEntry, 0, len(sourcesByName))
	for name, sources := range sourcesByName {
		entries = append(entries, TaskEventEntry{Name: name, Sources: sortedKeys(sources)})
	}
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Name < entries[right].Name })
	return entries, nil
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

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
