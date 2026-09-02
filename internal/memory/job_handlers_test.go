package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type fakeTaskRunReader struct {
	taskRuns map[string]taskstate.TaskRun
	steps    map[string][]taskstate.TaskStep
	events   map[string][]taskstate.TaskEvent
}

func (reader *fakeTaskRunReader) FindTaskRun(taskRunID string) (taskstate.TaskRun, bool) {
	taskRun, isFound := reader.taskRuns[taskRunID]
	return taskRun, isFound
}

func (reader *fakeTaskRunReader) ListTaskEvent(taskRunID string) []taskstate.TaskEvent {
	return reader.events[taskRunID]
}

func (reader *fakeTaskRunReader) AppendTaskEvent(taskRunID string, name string, body string) {
	reader.events[taskRunID] = append(reader.events[taskRunID], taskstate.TaskEvent{TaskRunID: taskRunID, Name: name, Body: body})
}

func (reader *fakeTaskRunReader) ListTaskStep(taskRunID string) []taskstate.TaskStep {
	return reader.steps[taskRunID]
}

func (reader *fakeTaskRunReader) eventNames(taskRunID string) []string {
	names := []string{}
	for _, event := range reader.events[taskRunID] {
		names = append(names, event.Name)
	}
	return names
}

type fakeAccessResolver struct{}

func (fakeAccessResolver) ResolvePersonAccess(personID string) policy.PersonAccess {
	return policy.PersonAccess{PersonID: personID, Circles: []string{"circle-platform"}, SecurityLevelRank: 2, GrantedClasses: []string{"finance"}}
}

func TestExtractJobHandlerTurnsAFinishedTaskIntoFacts(t *testing.T) {
	fixture := newIngestFixture()
	reader := &fakeTaskRunReader{
		taskRuns: map[string]taskstate.TaskRun{"run-1": {
			TaskRunID:            "run-1",
			RequesterPersonID:    "person-alice",
			OriginConversationID: "channel-platform",
			Status:               taskstate.TaskStatusCompleted,
			Prompt:               "다음 주 화요일 스탠드업을 10시로 옮겨줘, 나 플랫폼 팀으로 옮겼거든",
			Result:               "스탠드업을 10시로 옮겼습니다.",
			UpdatedAt:            ingestNow,
		}},
		steps:  map[string][]taskstate.TaskStep{"run-1": {{Instruction: "continue event_update", Status: taskstate.TaskStatusCompleted, Output: `{"eventID":"evt-1"}`}}},
		events: map[string][]taskstate.TaskEvent{"run-1": {{Name: memory.ExtractionContextEventName, Body: `{"requesterName":"이샘플","activeCircleID":"circle-platform","securityLevelRank":1,"requiredClasses":["finance"]}`}}},
	}
	fixture.model.Queue(memorytest.IngestResponse(
		memorytest.IngestFact{Content: "이샘플 works in the platform team", Kind: memory.FactKindIdentity, Scope: memory.ScopeTypeCircle, SubjectPersonHint: "이샘플", Relation: memory.FactRelationNew},
		memorytest.IngestFact{Content: "The platform standup is at 10:00", Kind: memory.FactKindFact, Scope: memory.ScopeTypeCircle, Relation: memory.FactRelationNew},
	))
	handler := memory.ExtractJobHandler{Ingester: fixture.ingester, TaskRuns: reader, Steps: reader, Access: fakeAccessResolver{}}
	if errorValue := handler.Handle(context.Background(), memory.Job{JobID: "job-1", Kind: memory.JobKindExtract, SubjectID: "run-1", Attempts: 1}); errorValue != nil {
		t.Fatalf("expected the extract job to succeed: %v", errorValue)
	}
	facts := fixture.repository.AllFacts()
	if len(facts) != 2 {
		t.Fatalf("expected two facts, got %+v", facts)
	}
	for _, fact := range facts {
		if fact.ScopeType != memory.ScopeTypeCircle || fact.ScopeID != "circle-platform" || fact.SecurityLevelRank != 1 || len(fact.RequiredClasses) != 1 {
			t.Fatalf("expected circle facts carrying the conversation label, got %+v", fact)
		}
	}
	if facts[0].SubjectPersonID != "person-alice" {
		t.Fatalf("expected the requester name to resolve to the requester, got %+v", facts[0])
	}
	transcript := fixture.model.LastUserMessage()
	for _, expected := range []string{"Request:\n다음 주 화요일", "Step (completed): continue event_update", `Output: {"eventID":"evt-1"}`, "Final reply:\n스탠드업을", "Outcome: completed", "Requester: 이샘플", "Active circle: circle-platform"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("expected the model to see %q, got:\n%s", expected, transcript)
		}
	}
	if names := reader.eventNames("run-1"); names[len(names)-1] != "memory.extraction_completed" {
		t.Fatalf("expected a completion event on the ledger, got %v", names)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(reader.events["run-1"][len(reader.events["run-1"])-1].Body), &body)
	if body["factCount"] != float64(2) || body["jobID"] != "job-1" {
		t.Fatalf("expected the event to carry counts, got %v", body)
	}
}

func TestExtractJobHandlerWithoutContextFallsBackToTheRequesterLabel(t *testing.T) {
	fixture := newIngestFixture()
	reader := &fakeTaskRunReader{
		taskRuns: map[string]taskstate.TaskRun{"run-2": {TaskRunID: "run-2", RequesterPersonID: "person-alice", Status: taskstate.TaskStatusFailed, Prompt: "do a thing", FailureReason: "tool unavailable", CreatedAt: ingestNow}},
		steps:    map[string][]taskstate.TaskStep{},
		events:   map[string][]taskstate.TaskEvent{},
	}
	fixture.model.Queue(memorytest.IngestResponse(memorytest.IngestFact{Content: "이샘플 asked for a thing", Kind: memory.FactKindEpisode, Scope: memory.ScopeTypeCircle, Relation: memory.FactRelationNew}))
	handler := memory.ExtractJobHandler{Ingester: fixture.ingester, TaskRuns: reader, Steps: reader, Access: fakeAccessResolver{}}
	if errorValue := handler.Handle(context.Background(), memory.Job{JobID: "job-2", Kind: memory.JobKindExtract, SubjectID: "run-2", Attempts: 1}); errorValue != nil {
		t.Fatal(errorValue)
	}
	fact := fixture.repository.AllFacts()[0]
	if fact.ScopeType != memory.ScopeTypePrivate || fact.SecurityLevelRank != 0 || len(fact.RequiredClasses) != 0 {
		t.Fatalf("expected a circle fact without context to narrow to an unlabelled private fact, got %+v", fact)
	}
}

func TestExtractJobHandlerRecordsTerminalFailuresOnTheLedger(t *testing.T) {
	fixture := newIngestFixture()
	reader := &fakeTaskRunReader{
		taskRuns: map[string]taskstate.TaskRun{"run-3": {TaskRunID: "run-3", RequesterPersonID: "person-alice", Status: taskstate.TaskStatusCompleted, Prompt: "p", UpdatedAt: ingestNow}},
		steps:    map[string][]taskstate.TaskStep{},
		events:   map[string][]taskstate.TaskEvent{},
	}
	fixture.model.Queue(memorytest.IngestResponse(memorytest.IngestFact{Content: "x", Kind: memory.FactKindFact, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationSupersedes, RelatedFactID: "invented"}))
	handler := memory.ExtractJobHandler{Ingester: fixture.ingester, TaskRuns: reader, Steps: reader, Access: fakeAccessResolver{}}
	if errorValue := handler.Handle(context.Background(), memory.Job{JobID: "job-3", Kind: memory.JobKindExtract, SubjectID: "run-3", Attempts: 1}); errorValue == nil {
		t.Fatal("expected the invented fact ID to fail the job")
	}
	if names := reader.eventNames("run-3"); len(names) != 1 || names[0] != "memory.extraction_failed" {
		t.Fatalf("expected a failure event, got %v", names)
	}
	var terminal memory.TerminalJobError
	if errorValue := handler.Handle(context.Background(), memory.Job{SubjectID: "missing"}); !errors.As(errorValue, &terminal) {
		t.Fatalf("expected a missing task run to fail terminally, got %v", errorValue)
	}
}

func TestTransitionObserverEnqueuesExtractionForTerminalRunsOnly(t *testing.T) {
	repository := memory.NewInMemoryRepository()
	observer := memory.TaskRunTransitionObserver{Store: memory.Store{Jobs: repository, Now: func() time.Time { return ingestNow }}}
	observer.Observe(taskstate.TaskRun{TaskRunID: "running", RequesterPersonID: "p", Prompt: "x", Status: taskstate.TaskStatusRunning})
	observer.Observe(taskstate.TaskRun{TaskRunID: "no-prompt", RequesterPersonID: "p", Status: taskstate.TaskStatusCompleted})
	observer.Observe(taskstate.TaskRun{TaskRunID: "done", RequesterPersonID: "p", Prompt: "x", Status: taskstate.TaskStatusCompleted})
	observer.Observe(taskstate.TaskRun{TaskRunID: "done", RequesterPersonID: "p", Prompt: "x", Status: taskstate.TaskStatusCompleted})
	observer.Observe(taskstate.TaskRun{TaskRunID: "cancelled", RequesterPersonID: "p", Prompt: "x", Status: taskstate.TaskStatusCancelled})
	jobs, _ := repository.ClaimDueJobs(context.Background(), []string{memory.JobKindExtract}, ingestNow, time.Minute, 10)
	subjects := []string{}
	for _, job := range jobs {
		subjects = append(subjects, job.SubjectID)
	}
	if len(subjects) != 2 || !strings.Contains(strings.Join(subjects, ","), "done") || !strings.Contains(strings.Join(subjects, ","), "cancelled") {
		t.Fatalf("expected one job each for the finished and cancelled runs, got %v", subjects)
	}
}

func TestProfileBuilderCondensesFactsAndSkipsTheModelWhenEmpty(t *testing.T) {
	fixture := newIngestFixture()
	builder := memory.ProfileBuilder{Store: fixture.ingester.Store, Model: fixture.model, Now: func() time.Time { return ingestNow }}
	empty, errorValue := builder.Rebuild(context.Background(), "person-nobody")
	if errorValue != nil || len(empty.IdentityLines) != 0 || fixture.model.RequestCount() != 0 {
		t.Fatalf("expected an empty profile without a model call, got %+v (%v)", empty, errorValue)
	}
	fixture.ingest(t, "이샘플 works in the platform team and prefers bullet summaries", memorytest.IngestResponse(
		memorytest.IngestFact{Content: "이샘플 works in the platform team", Kind: memory.FactKindIdentity, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew},
		memorytest.IngestFact{Content: "이샘플 is migrating admind config this week", Kind: memory.FactKindFact, Scope: memory.ScopeTypePrivate, Relation: memory.FactRelationNew},
	), nil)
	fixture.model.Queue(memorytest.ProfileResponse([]string{"이샘플 is on the platform team"}, []string{"이샘플 is migrating admind config", "", "  "}))
	profile, errorValue := builder.Rebuild(context.Background(), "person-alice")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(profile.IdentityLines) != 1 || len(profile.CurrentLines) != 1 || profile.BuiltFromFactCount != 2 {
		t.Fatalf("expected a condensed profile, got %+v", profile)
	}
	if !strings.Contains(fixture.model.LastUserMessage(), "[identity, 2026-09-02] 이샘플 works in the platform team") {
		t.Fatalf("expected the facts listed to the model, got %s", fixture.model.LastUserMessage())
	}
	saved, isFound, _ := fixture.repository.FindProfile(context.Background(), "person-alice")
	if !isFound || saved.IdentityLines[0] != "이샘플 is on the platform team" {
		t.Fatalf("expected the profile saved, got %+v", saved)
	}
}

func TestBudgetRecallStopsAtTheCeilings(t *testing.T) {
	long := strings.Repeat("가", 100)
	recall := memory.Recall{
		Profile: memory.Profile{IdentityLines: []string{long, long, long}, CurrentLines: []string{long}},
		Facts:   []memory.ScoredFact{{Fact: memory.Fact{Content: long}}, {Fact: memory.Fact{Content: long}}, {Fact: memory.Fact{Content: long}}},
	}
	budgeted := memory.BudgetRecall(recall, 250, 150)
	if len(budgeted.Profile.IdentityLines) != 2 || len(budgeted.Profile.CurrentLines) != 0 {
		t.Fatalf("expected the profile trimmed to the budget, got %+v", budgeted.Profile)
	}
	if len(budgeted.Facts) != 1 {
		t.Fatalf("expected one recalled fact within budget, got %d", len(budgeted.Facts))
	}
}

func TestRenderTaskTranscriptClampsLongSteps(t *testing.T) {
	taskRun := taskstate.TaskRun{Prompt: "p", Status: taskstate.TaskStatusCompleted, Result: "r"}
	steps := []taskstate.TaskStep{{Instruction: strings.Repeat("a", 5000), Output: strings.Repeat("b", 5000), Status: taskstate.TaskStatusCompleted}}
	transcript := memory.RenderTaskTranscript(taskRun, steps)
	if len([]rune(transcript)) > 2700 || !strings.Contains(transcript, "Final reply:\nr") {
		t.Fatalf("expected a clamped transcript with the reply, got %d runes", len([]rune(transcript)))
	}
}
