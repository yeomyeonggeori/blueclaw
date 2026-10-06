package memory_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestWhatARunsToolAlreadyAnsweredIsNotRememberedForThePerson(t *testing.T) {
	stores := memorytest.OpenJudgingSame(t)

	remember(t, stores, "run-1", toolAnswered("observation-1", "The weekly review is on Monday at 10:00"))

	if count := memorytest.Count(t, stores, memory.PersonScope("person-1")); count != 0 {
		t.Fatalf("a person's memory took %d copies of what the calendar answered", count)
	}
}

func TestAFailedToolIsNotTakenAsAnAnswer(t *testing.T) {
	stores := memorytest.OpenJudgingSame(t)

	remember(t, stores, "run-1", toolFailed("observation-1", "The weekly review is on Monday at 10:00"))

	if count := memorytest.Count(t, stores, memory.PersonScope("person-1")); count != 1 {
		t.Fatalf("a run with only a failed tool left %d memories, so a failure would read as an answer", count)
	}
}

func remember(t *testing.T, stores *memory.Stores, taskRunID string, events ...agentcontract.TaskEvent) {
	t.Helper()
	taskRuns := &recordedRun{events: events}
	memory.TaskRunTransitionObserver{Stores: stores, TaskRuns: taskRuns}.Observe(agentcontract.TaskRun{
		TaskRunID:         taskRunID,
		RequesterPersonID: "person-1",
		Prompt:            "When is the weekly review?",
		Result:            "The weekly review is on Monday at 10:00",
		Status:            agentcontract.TaskStatusCompleted,
	})
	deadline := time.Now().Add(10 * time.Second)
	for !taskRuns.has("memory.extraction_completed") {
		if failure := taskRuns.body("memory.extraction_failed"); failure != "" {
			t.Fatalf("remembering failed: %s", failure)
		}
		if time.Now().After(deadline) {
			t.Fatal("the run was never remembered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func toolAnswered(observationID string, summary string) agentcontract.TaskEvent {
	return toolResult(map[string]any{"observationID": observationID, "summary": summary})
}

func toolFailed(observationID string, summary string) agentcontract.TaskEvent {
	return toolResult(map[string]any{"observationID": observationID, "summary": summary, "failure": map[string]any{"kind": "unknown"}})
}

func toolResult(observation map[string]any) agentcontract.TaskEvent {
	body, _ := json.Marshal(observation)
	return agentcontract.TaskEvent{Name: agentcontract.ToolTaskEventName("calendar_event_list", agentcontract.ToolTaskEventResultSuffix), Body: string(body)}
}

type recordedRun struct {
	mutex  sync.Mutex
	events []agentcontract.TaskEvent
}

func (run *recordedRun) FindTaskRun(string) (agentcontract.TaskRun, bool) {
	return agentcontract.TaskRun{}, false
}

func (run *recordedRun) ListTaskEvent(string) []agentcontract.TaskEvent {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	return append([]agentcontract.TaskEvent{}, run.events...)
}

func (run *recordedRun) AppendTaskEvent(_ string, name string, body string) {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	run.events = append(run.events, agentcontract.TaskEvent{Name: name, Body: body})
}

func (run *recordedRun) has(name string) bool {
	for _, event := range run.ListTaskEvent("") {
		if event.Name == name {
			return true
		}
	}
	return false
}

func (run *recordedRun) body(name string) string {
	for _, event := range run.ListTaskEvent("") {
		if event.Name == name {
			return event.Body
		}
	}
	return ""
}
