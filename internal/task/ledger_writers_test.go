package task

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const holdRecordPackagePath = "../../.dependency/blueprotocol/holdrecord"

const bundledHarnessPath = "../../.dependency/bluecollar"

var eventNamesOwnedByTheHoldRecord = []string{
	"approval.hold_opened",
	"approval.decided",
	"approval.hold_spent",
	"approval.scope_granted",
}

const taskEventNameDeclarationPath = "../../.dependency/blueprotocol/agentcontract/task_event_name.go"

var taskEventWriteCall = regexp.MustCompile(`(?s:(?:AppendTaskEvent|appendEvent|appendTaskEvent)\(\s*[^,]+,\s*(?:agentcontract\.)?(TaskEvent[A-Za-z0-9]+))`)

var taskEventNameDeclaration = regexp.MustCompile(`(TaskEvent[A-Za-z0-9]+)\s+= "([a-z0-9_.]+)"`)

func declaredTaskEventNames(t *testing.T) map[string]string {
	t.Helper()
	source, readError := os.ReadFile(filepath.FromSlash(taskEventNameDeclarationPath))
	if readError != nil {
		t.Fatalf("reading %s: %v", taskEventNameDeclarationPath, readError)
	}
	names := map[string]string{}
	for _, match := range taskEventNameDeclaration.FindAllStringSubmatch(string(source), -1) {
		names[match[1]] = match[2]
	}
	if len(names) == 0 {
		t.Fatalf("no task event names declared in %s", taskEventNameDeclarationPath)
	}
	return names
}

func eventNamesWrittenUnder(t *testing.T, rootPath string) map[string]bool {
	t.Helper()
	return eventNamesWrittenUnderExcept(t, rootPath, "")
}

func eventNamesWrittenUnderExcept(t *testing.T, rootPath string, skippedPath string) map[string]bool {
	t.Helper()
	declaredNames := declaredTaskEventNames(t)
	writtenNames := map[string]bool{}
	errorValue := filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if entry.IsDir() && skippedPath != "" && filepath.Clean(path) == filepath.Clean(skippedPath) {
			return filepath.SkipDir
		}
		if entry.IsDir() && entry.Name() == ".dependency" && filepath.Clean(path) != filepath.Clean(rootPath) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readError := os.ReadFile(path)
		if readError != nil {
			return readError
		}
		for _, match := range taskEventWriteCall.FindAllStringSubmatch(string(source), -1) {
			if name, isDeclared := declaredNames[match[1]]; isDeclared {
				writtenNames[name] = true
			}
		}
		return nil
	})
	if errorValue != nil {
		t.Fatalf("walking %s: %v", rootPath, errorValue)
	}
	return writtenNames
}

func TestEveryEventNameHasOneWriter(t *testing.T) {
	hostNames := eventNamesWrittenUnder(t, "../../internal")
	for name := range eventNamesWrittenUnder(t, "../../cmd") {
		hostNames[name] = true
	}
	loopNames := eventNamesWrittenUnderBundledHarness(t)

	writtenOnBothSides := []string{}
	for name := range hostNames {
		if loopNames[name] {
			writtenOnBothSides = append(writtenOnBothSides, name)
		}
	}
	sort.Strings(writtenOnBothSides)

	if len(writtenOnBothSides) > 0 {
		t.Fatalf("%v are written by the host and by the agent loop, so which one fires depends on the configured harness; give each one owner and the other a name of its own", writtenOnBothSides)
	}
}

func TestTheHoldEventsAreWrittenOnlyByTheHoldRecord(t *testing.T) {
	writtenOutsideTheRecord := eventNamesWrittenUnder(t, "../../internal")
	for name := range eventNamesWrittenUnder(t, "../../cmd") {
		writtenOutsideTheRecord[name] = true
	}
	bluecollarWriters := eventNamesWrittenUnderBundledHarnessExcept(t, holdRecordPackagePath)
	for name := range bluecollarWriters {
		writtenOutsideTheRecord[name] = true
	}
	for _, name := range eventNamesOwnedByTheHoldRecord {
		if writtenOutsideTheRecord[name] {
			t.Fatalf("%q is written outside blueprotocol's holdrecord; open, decide and spend a hold through that package", name)
		}
	}
	if recordWrites := eventNamesWrittenUnder(t, holdRecordPackagePath); len(recordWrites) != len(eventNamesOwnedByTheHoldRecord) {
		t.Fatalf("holdrecord writes %v, expected exactly %v", recordWrites, eventNamesOwnedByTheHoldRecord)
	}
}

func eventNamesWrittenUnderBundledHarness(t *testing.T) map[string]bool {
	t.Helper()
	if _, statError := os.Stat(bundledHarnessPath); statError != nil {
		return map[string]bool{}
	}
	return eventNamesWrittenUnder(t, bundledHarnessPath)
}

func eventNamesWrittenUnderBundledHarnessExcept(t *testing.T, excludedPath string) map[string]bool {
	t.Helper()
	if _, statError := os.Stat(bundledHarnessPath); statError != nil {
		return map[string]bool{}
	}
	return eventNamesWrittenUnderExcept(t, bundledHarnessPath, excludedPath)
}
