package acpharness

import (
	"context"
	"encoding/json"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func newTaskRunStore() (*taskstate.TaskRunService, string) {
	taskRuns := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRun := taskRuns.CreateTaskRunWithOrigin("person-1", taskstate.TaskRunOrigin{}, "회의록 정리해줘")
	return taskRuns, taskRun.TaskRunID
}

func ledgerToolCall(eventName string, body string) acp.SessionUpdate {
	update := acp.StartToolCall("call-1", "note_write", acp.WithStartStatus(acp.ToolCallStatusPending))
	update.ToolCall.Meta = map[string]any{agentcontract.LedgerMetaKey: agentcontract.LedgerRecord{Name: eventName, Body: json.RawMessage(body), Text: body}}
	return update
}

func eventNamesOf(taskRuns taskstate.TaskRunStore, taskRunID string) []string {
	names := []string{}
	for _, taskEvent := range taskRuns.ListTaskEvent(taskRunID) {
		names = append(names, taskEvent.Name)
	}
	return names
}

func runTurnWithLedgerUpdates(t *testing.T, skippedEventNames []string, updates ...acp.SessionUpdate) (*taskstate.TaskRunService, string, []acp.SessionUpdate) {
	t.Helper()
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{toolCallUpdates: updates}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns)
	harness.UseLedgerExchange(skippedEventNames)
	narrated := []acp.SessionUpdate{}
	ctx := toolcallprogress.WithObserver(context.Background(), func(update acp.SessionUpdate) { narrated = append(narrated, update) })
	executed := []daemonExecutedTool{}

	if _, errorValue := harness.RunTurn(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRunID,
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	}); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return taskRuns, taskRunID, narrated
}

func TestARecordTheAgentSendsOnItsLedgerLandsOnTheRunTheHostOwns(t *testing.T) {
	taskRuns, taskRunID, _ := runTurnWithLedgerUpdates(t, nil, ledgerToolCall("tool.note_write.requested", `{"observationID":"o1","input":{"text":"b","a":"a"}}`))

	events := taskRuns.ListTaskEvent(taskRunID)
	last := events[len(events)-1]
	if last.Name != "tool.note_write.requested" || last.Body != `{"observationID":"o1","input":{"text":"b","a":"a"}}` {
		t.Fatalf("the agent's record has to reach the host's run exactly as written, got %+v", last)
	}
}

func TestARecordTheHostWritesItselfIsNotMirroredASecondTime(t *testing.T) {
	taskRuns, taskRunID, _ := runTurnWithLedgerUpdates(t, []string{"task.steer.requested"}, ledgerToolCall("task.steer.requested", `{"instruction":"stop"}`))

	for _, name := range eventNamesOf(taskRuns, taskRunID) {
		if name == "task.steer.requested" {
			t.Fatalf("the host appended this record itself and mirroring it doubles what the admin view shows, got %v", eventNamesOf(taskRuns, taskRunID))
		}
	}
}

func TestAToolCallTheLedgerAlreadyCarriesIsNotNarratedTwice(t *testing.T) {
	_, _, narrated := runTurnWithLedgerUpdates(t, nil, ledgerToolCall("tool.note_write.requested", `{"observationID":"o1"}`))

	if len(narrated) != 0 {
		t.Fatalf("the mirrored record reaches the run's own subscription, so narrating the update as well says the call twice, got %d", len(narrated))
	}
}

func TestAToolCallWithoutALedgerRecordIsStillNarrated(t *testing.T) {
	_, _, narrated := runTurnWithLedgerUpdates(t, nil, acp.StartToolCall("call-1", "Read notes.md", acp.WithStartStatus(acp.ToolCallStatusInProgress)))

	if len(narrated) != 1 {
		t.Fatalf("an update the ledger does not carry has no other way to reach the person, got %d", len(narrated))
	}
}

func replayedRecords(t *testing.T, request agentcontract.AgentTurnRequest, taskRuns *taskstate.TaskRunService) []agentcontract.LedgerRecord {
	t.Helper()
	agent := &externalAgent{}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns)
	harness.UseLedgerExchange(nil)
	executed := []daemonExecutedTool{}
	request.RequesterPersonID = "person-1"
	request.Prompt = "회의록 정리해줘"
	request.WorkspaceRootPath = t.TempDir()
	request.ToolSet = requesterToolSet(t, "person-1", &executed)
	if _, errorValue := harness.RunTurn(context.Background(), request); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	encoded, _ := json.Marshal(agent.observedPromptMeta[agentcontract.LedgerMetaKey])
	records := []agentcontract.LedgerRecord{}
	_ = json.Unmarshal(encoded, &records)
	return records
}

func TestARelaunchedRunSendsItsRecordsBackToTheAgent(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	taskRuns.AppendTaskEvent(taskRunID, "agent.action", `{"b":1,"a":2}`)

	records := replayedRecords(t, agentcontract.AgentTurnRequest{ExistingTaskRunID: taskRunID}, taskRuns)

	if len(records) == 0 || records[len(records)-1].EventBody() != `{"b":1,"a":2}` {
		t.Fatalf("the run's records have to go back to the agent as they were written, got %+v", records)
	}
}

func TestARunOpenedForThisTurnHasNothingToReplay(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	taskRuns.AppendTaskEvent(taskRunID, "agent.task_launched", `{}`)

	records := replayedRecords(t, agentcontract.AgentTurnRequest{ExistingTaskRunID: taskRunID, IsTaskRunOpenedForThisTurn: true}, taskRuns)

	if len(records) != 0 {
		t.Fatalf("replaying a run's own launch records tells the agent it was restarted, got %+v", records)
	}
}

func mirrorACancellation(t *testing.T, hostRecordedIt bool) []string {
	t.Helper()
	taskRuns, taskRunID := newTaskRunStore()
	if hostRecordedIt {
		taskRuns.AppendTaskEvent(taskRunID, "tool.note_write.cancelled", `{"observationID":"obs-001","reason":"cancelled_by_attempt_end"}`)
	}
	mirror := ledgerMirror{exchange: newLedgerExchange(nil), taskRunStore: taskRuns, taskRunID: taskRunID}
	mirror.take(agentcontract.LedgerRecord{Name: "tool.note_write.cancelled", Body: json.RawMessage(`{"observationID":"obs-001","reason":"cancelled_by_attempt_end"}`)})
	return eventNamesOf(taskRuns, taskRunID)
}

func TestACancellationTheHostAlreadyRecordedIsNotMirroredASecondTime(t *testing.T) {
	cancellations := 0
	for _, name := range mirrorACancellation(t, true) {
		if name == "tool.note_write.cancelled" {
			cancellations++
		}
	}
	if cancellations != 1 {
		t.Fatalf("the host ended the attempt that held the call and wrote the cancellation, so the agent's is the same fact, got %d", cancellations)
	}
}

func TestACancellationOnlyTheAgentRecordedIsMirrored(t *testing.T) {
	cancellations := 0
	for _, name := range mirrorACancellation(t, false) {
		if name == "tool.note_write.cancelled" {
			cancellations++
		}
	}
	if cancellations != 1 {
		t.Fatalf("a cancellation nobody else wrote is the agent's to report, got %d", cancellations)
	}
}

func TestACancellationOfAnotherCallIsMirroredBesideTheHostsOwn(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	taskRuns.AppendTaskEvent(taskRunID, "tool.note_write.cancelled", `{"observationID":"obs-001"}`)
	mirror := ledgerMirror{exchange: newLedgerExchange(nil), taskRunStore: taskRuns, taskRunID: taskRunID}

	mirror.take(agentcontract.LedgerRecord{Name: "tool.note_write.cancelled", Body: json.RawMessage(`{"observationID":"obs-002"}`)})

	if len(eventNamesOf(taskRuns, taskRunID)) != 3 {
		t.Fatalf("each call's cancellation is its own record, got %v", eventNamesOf(taskRuns, taskRunID))
	}
}
