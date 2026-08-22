package sessionquery

import (
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type ledgerStub struct {
	taskRunsByPersonID map[string][]task.TaskRun
	eventsByTaskRunID  map[string][]task.TaskEvent
}

func (stub ledgerStub) ListTaskRunByPersonID(personID string) []task.TaskRun {
	return stub.taskRunsByPersonID[personID]
}

func (stub ledgerStub) ListTaskEvent(taskRunID string) []task.TaskEvent {
	return stub.eventsByTaskRunID[taskRunID]
}

func twoPeopleLedger() ledgerStub {
	return ledgerStub{
		taskRunsByPersonID: map[string][]task.TaskRun{
			"person-1": {
				{TaskRunID: "run-1", OriginConversationID: "conversation-1", Status: task.TaskStatusCompleted, Prompt: "회의록 보내줘", CreatedAt: time.Unix(1, 0)},
				{TaskRunID: "run-2", OriginConversationID: "conversation-2", Status: task.TaskStatusFailed, Prompt: "캘린더에 등록해줘", FailureReason: "calendar refused the entry", CreatedAt: time.Unix(2, 0)},
			},
			"person-2": {
				{TaskRunID: "run-3", OriginConversationID: "conversation-3", Status: task.TaskStatusCompleted, Prompt: "회의록 보내줘", CreatedAt: time.Unix(3, 0)},
			},
		},
		eventsByTaskRunID: map[string][]task.TaskEvent{
			"run-1": {{Name: "tool.message_send.result", Body: `{"output":{"content":"sent to 박예시"}}`}},
		},
	}
}

func TestASearchReadsOnlyTheLedgerOfThePersonItNames(t *testing.T) {
	service := New(twoPeopleLedger())

	result, errorValue := service.Search(Request{RequesterPersonID: "person-1", Text: "회의록"})

	if errorValue != nil {
		t.Fatalf("searching failed: %v", errorValue)
	}
	if len(result.Matches) != 1 || result.Matches[0].TaskRunID != "run-1" {
		t.Fatalf("the ledger holds message text and tool inputs from everyone on this device, so a search reaches one person's runs and no further: %+v", result.Matches)
	}
}

func TestASearchThatNamesNobodyIsRefused(t *testing.T) {
	service := New(twoPeopleLedger())

	if _, errorValue := service.Search(Request{Text: "회의록"}); errorValue == nil {
		t.Fatal("a search with no person is a search of everyone's ledger, and there is no safe default for that")
	}
}

func TestASearchReachesIntoEventBodiesAndSaysWhereItMatched(t *testing.T) {
	service := New(twoPeopleLedger())

	result, errorValue := service.Search(Request{RequesterPersonID: "person-1", Text: "박예시"})

	if errorValue != nil {
		t.Fatalf("searching failed: %v", errorValue)
	}
	if len(result.Matches) != 1 || strings.Join(result.Matches[0].MatchedIn, ",") != "tool.message_send.result" {
		t.Fatalf("what a run actually did lives in its events, and a hit has to say which one it was: %+v", result.Matches)
	}

	failed, _ := service.Search(Request{RequesterPersonID: "person-1", Text: "calendar refused"})
	if len(failed.Matches) != 1 || strings.Join(failed.Matches[0].MatchedIn, ",") != "failureReason" {
		t.Fatalf("expected the failure reason to be searchable: %+v", failed.Matches)
	}
}

func TestEveryReadIsBoundedAndSaysWhatItLeftOut(t *testing.T) {
	taskRuns := []task.TaskRun{}
	for index := 0; index < 40; index++ {
		taskRuns = append(taskRuns, task.TaskRun{TaskRunID: "run", Prompt: "회의록 보내줘"})
	}
	service := New(ledgerStub{taskRunsByPersonID: map[string][]task.TaskRun{"person-1": taskRuns}})

	result, errorValue := service.Search(Request{RequesterPersonID: "person-1", Text: "회의록", Limit: 5})

	if errorValue != nil {
		t.Fatalf("searching failed: %v", errorValue)
	}
	if len(result.Matches) != 5 || result.TotalMatched != 40 || !result.IsTruncated {
		t.Fatalf("an unbounded ledger read into a turn's context is a way to lose the turn, and a silent truncation reads as covering everything: %+v", result)
	}
	if boundedLimit(5000) != maximumLimit || boundedLimit(0) != defaultLimit {
		t.Fatal("the caller does not get to ask for the whole ledger, and asking for nothing does not mean all of it")
	}
}

func TestAConversationIsTheLineageThisProductHas(t *testing.T) {
	service := New(twoPeopleLedger())

	result, errorValue := service.Search(Request{RequesterPersonID: "person-1", ConversationID: "conversation-2"})

	if errorValue != nil {
		t.Fatalf("searching failed: %v", errorValue)
	}
	if len(result.Matches) != 1 || result.Matches[0].TaskRunID != "run-2" {
		t.Fatalf("a follow-up is a new run in the same conversation, so that is how a task's history is walked: %+v", result.Matches)
	}
}

type countingLedger struct {
	taskRuns  []task.TaskRun
	readCount int
}

func (ledger *countingLedger) ListTaskRunByPersonID(string) []task.TaskRun { return ledger.taskRuns }

func (ledger *countingLedger) ListTaskEvent(string) []task.TaskEvent {
	ledger.readCount++
	return nil
}

func TestOneSearchCannotIssueOneQueryPerTaskRunEverRecorded(t *testing.T) {
	taskRuns := []task.TaskRun{}
	for index := 0; index < 2000; index++ {
		taskRuns = append(taskRuns, task.TaskRun{TaskRunID: "run", Prompt: "무언가"})
	}
	ledger := &countingLedger{taskRuns: taskRuns}

	result, errorValue := New(ledger).Search(Request{RequesterPersonID: "person-1", Text: "회의록"})

	if errorValue != nil {
		t.Fatalf("searching failed: %v", errorValue)
	}
	if ledger.readCount > maximumScannedTaskRuns {
		t.Fatalf("reading a run's events is a query per run against Postgres, so an unbounded scan is an unbounded query fan-out: %d reads", ledger.readCount)
	}
	if !result.DidStopScanning {
		t.Fatal("a scan that stopped early has to say so, or a caller reads no matches as nothing happened")
	}
	if result.Scanned != maximumScannedTaskRuns {
		t.Fatalf("expected the scan to stop at its ceiling, got %d", result.Scanned)
	}
}

func TestASearchThatReachedTheEndSaysItDidNotStop(t *testing.T) {
	result, errorValue := New(twoPeopleLedger()).Search(Request{RequesterPersonID: "person-1", Text: "회의록"})

	if errorValue != nil {
		t.Fatalf("searching failed: %v", errorValue)
	}
	if result.DidStopScanning {
		t.Fatal("a ledger smaller than the ceiling was read whole, and saying otherwise sends the reader looking for pages that do not exist")
	}
}
