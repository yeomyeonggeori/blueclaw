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

const approvalGateWritersReason = "one approval gate is installed per harness, so the host gate and bluecollar's approval package never write in the same task run; the meta-harness migration removes the host gate"

const holdRecordPackagePath = "../../.dependency/bluecollar/holdrecord"

var eventNamesOwnedByTheHoldRecord = []string{
	"approval.hold_opened",
	"approval.decided",
	"approval.hold_spent",
	"approval.scope_granted",
}

var eventNamesWrittenOnBothSides = map[string]string{
	"ask.requested":               "undecided: the host writes the approval question and the loop writes its own ask_input question, which may be two events of one kind rather than one event with two writers",
	"approval.wording_failed":     approvalGateWritersReason,
	"confirmation.requested":      approvalGateWritersReason,
	"agent.failure_reply":         "the host, which sees every turn result",
	"agent.failure_report":        "the host, which sees every turn result",
	"agent.limit_reply":           "the host, which sees every turn result",
	"agent.limit_stop":            "the host, which sees every turn result",
	"agent.goal.blocked":          "the host, which sees every turn result",
	"task.stop.outbox_suppressed": "the host, which is what cancelled the run",
	"task.steer.requested":        "a run's ledger holds one record per steer, the host's. The two writers are the host (connectors busy_message appends it to the run) and bluecollar's ACP agent (acpagent steer.go appends its own copy to the agent's private store when the host forwards the steer; the in-process loop no longer writes it, but acpagent still does, so the two writers remain); the mirror skips the name (internal/bluecollaracp), so the agent's copy never reaches the run",
}

const taskEventNameDeclarationPath = "../../.dependency/bluecollar/agentcontract/task_event_name.go"

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

func TestNoNewEventNameGainsASecondWriter(t *testing.T) {
	hostNames := eventNamesWrittenUnder(t, "../../internal")
	for name := range eventNamesWrittenUnder(t, "../../cmd") {
		hostNames[name] = true
	}
	loopNames := eventNamesWrittenUnder(t, "../../.dependency/bluecollar")

	writtenOnBothSides := []string{}
	for name := range hostNames {
		if loopNames[name] {
			writtenOnBothSides = append(writtenOnBothSides, name)
		}
	}
	sort.Strings(writtenOnBothSides)

	for _, name := range writtenOnBothSides {
		if _, isKnown := eventNamesWrittenOnBothSides[name]; !isKnown {
			t.Fatalf("%q is now written by the host and by the agent loop, so which one fires depends on the configured harness; give it one owner or add it to eventNamesWrittenOnBothSides with where that owner will be", name)
		}
	}
	for name := range eventNamesWrittenOnBothSides {
		if hostNames[name] && loopNames[name] {
			continue
		}
		t.Fatalf("%q has one writer again; drop it from eventNamesWrittenOnBothSides", name)
	}
}

func TestTheHoldEventsAreWrittenOnlyByTheHoldRecord(t *testing.T) {
	writtenOutsideTheRecord := eventNamesWrittenUnder(t, "../../internal")
	for name := range eventNamesWrittenUnder(t, "../../cmd") {
		writtenOutsideTheRecord[name] = true
	}
	bluecollarWriters := eventNamesWrittenUnderExcept(t, "../../.dependency/bluecollar", holdRecordPackagePath)
	for name := range bluecollarWriters {
		writtenOutsideTheRecord[name] = true
	}
	for _, name := range eventNamesOwnedByTheHoldRecord {
		if writtenOutsideTheRecord[name] {
			t.Fatalf("%q is written outside bluecollar's holdrecord; open, decide and spend a hold through that package", name)
		}
	}
	if recordWrites := eventNamesWrittenUnder(t, holdRecordPackagePath); len(recordWrites) != len(eventNamesOwnedByTheHoldRecord) {
		t.Fatalf("holdrecord writes %v, expected exactly %v", recordWrites, eventNamesOwnedByTheHoldRecord)
	}
}
