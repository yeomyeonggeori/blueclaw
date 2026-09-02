package integration

import (
	"context"
	"github.com/yeomyeonggeori/bluememo"
	"github.com/yeomyeonggeori/bluememo/bluememotest"
	bluememopostgres "github.com/yeomyeonggeori/bluememo/postgres"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

type integrationTaskRunReader struct {
	taskRuns map[string]taskstate.TaskRun
	events   map[string][]taskstate.TaskEvent
}

func (reader *integrationTaskRunReader) FindTaskRun(taskRunID string) (taskstate.TaskRun, bool) {
	taskRun, isFound := reader.taskRuns[taskRunID]
	return taskRun, isFound
}

func (reader *integrationTaskRunReader) ListTaskEvent(taskRunID string) []taskstate.TaskEvent {
	return reader.events[taskRunID]
}

func (reader *integrationTaskRunReader) AppendTaskEvent(taskRunID string, name string, body string) {
	reader.events[taskRunID] = append(reader.events[taskRunID], taskstate.TaskEvent{TaskRunID: taskRunID, Name: name, Body: body})
}

func (reader *integrationTaskRunReader) ListTaskStep(string) []taskstate.TaskStep {
	return nil
}

type integrationAccessResolver struct {
	personAccess policy.PersonAccess
}

func (resolver integrationAccessResolver) ResolvePersonAccess(string) policy.PersonAccess {
	return resolver.personAccess
}

func (resolver integrationAccessResolver) ContainedCircles() map[string][]string {
	return map[string][]string{}
}

func TestMemoryExtractionRecordsFactsAndProfileInPostgres(t *testing.T) {
	fixture := openMemoryStoreFixture(t)
	ctx := context.Background()
	alice := fixture.addPerson(t, "alice")
	scripted := bluememotest.NewScriptedModel()
	store := bluememo.Store{
		Facts:          fixture.facts,
		Profiles:       bluememopostgres.NewProfileRepository(fixture.database.SQL),
		Jobs:           fixture.jobs,
		Embedder:       &bluememotest.HashEmbedder{},
		EmbeddingModel: "test-embed",
		Now:            func() time.Time { return fixture.now },
	}
	ingester := bluememo.Ingester{Store: store, Model: scripted, Now: func() time.Time { return fixture.now }}
	reader := &integrationTaskRunReader{
		taskRuns: map[string]taskstate.TaskRun{"run-1": {
			TaskRunID:         "run-1",
			RequesterPersonID: alice,
			Status:            taskstate.TaskStatusCompleted,
			Prompt:            "나 플랫폼 팀으로 옮겼어. 앞으로 요약은 불릿으로 해줘.",
			Result:            "알겠습니다.",
			UpdatedAt:         fixture.now,
		}},
		events: map[string][]taskstate.TaskEvent{"run-1": {{Name: memory.ExtractionContextEventName, Body: `{"requesterName":"이샘플","securityLevelRank":1,"requiredClasses":[]}`}}},
	}
	access := integrationAccessResolver{personAccess: policy.PersonAccess{PersonID: alice, SecurityLevelRank: 1}}
	worker := bluememo.JobWorker{
		Jobs: fixture.jobs,
		Now:  func() time.Time { return fixture.now },
		Handlers: map[string]bluememo.JobHandler{
			bluememo.JobKindExtract: memory.ExtractJobHandler{Ingester: ingester, TaskRuns: reader, Steps: reader, Access: access}.Handle,
			bluememo.JobKindProfile: bluememo.ProfileJobHandler{Builder: bluememo.ProfileBuilder{Store: store, Model: scripted, Now: func() time.Time { return fixture.now }}}.Handle,
		},
	}

	memory.TaskRunTransitionObserver{Store: store}.Observe(reader.taskRuns["run-1"])
	scripted.Queue(bluememotest.IngestResponse(
		bluememotest.IngestFact{Content: "이샘플 works in the platform team", Kind: bluememo.FactKindIdentity, Scope: bluememo.ScopeTypePrivate, SubjectPersonHint: "이샘플", Relation: bluememo.FactRelationNew},
		bluememotest.IngestFact{Content: "이샘플 prefers bullet summaries", Kind: bluememo.FactKindPreference, Scope: bluememo.ScopeTypePrivate, SubjectPersonHint: "이샘플", Relation: bluememo.FactRelationNew},
	))
	if runCount, errorValue := worker.RunOnce(ctx); errorValue != nil || runCount != 1 {
		t.Fatalf("expected the extraction job to run once, got %d (%v)", runCount, errorValue)
	}
	scripted.Queue(bluememotest.ProfileResponse([]string{"이샘플 is on the platform team and wants bullet summaries"}, []string{}))
	if runCount, errorValue := worker.RunOnce(ctx); errorValue != nil || runCount != 1 {
		t.Fatalf("expected the profile job to run once, got %d (%v)", runCount, errorValue)
	}

	var factCount, episodeCount int
	if errorValue := fixture.database.SQL.QueryRow(`SELECT (SELECT count(*) FROM memory_fact), (SELECT count(*) FROM memory_episode WHERE source_kind = 'task_run' AND source_id = 'run-1')`).Scan(&factCount, &episodeCount); errorValue != nil {
		t.Fatal(errorValue)
	}
	if factCount != 2 || episodeCount != 1 {
		t.Fatalf("expected two facts under one task episode, got facts=%d episodes=%d", factCount, episodeCount)
	}
	recall, errorValue := store.Recall(ctx, bluememo.RecallRequest{Reader: bluememo.NewReader(alice, nil, nil, 1, nil), PersonID: alice, Query: "요약 bullet summaries"})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(recall.Profile.IdentityLines) != 1 || recall.Profile.BuiltFromFactCount != 2 {
		t.Fatalf("expected the rebuilt profile, got %+v", recall.Profile)
	}
	if len(recall.Facts) == 0 || recall.Facts[0].Fact.Content != "이샘플 prefers bullet summaries" {
		t.Fatalf("expected the preference recalled first, got %+v", recall.Facts)
	}
	if events := reader.events["run-1"]; events[len(events)-1].Name != "memory.extraction_completed" {
		t.Fatalf("expected the ledger to carry the completion, got %+v", events)
	}
	if pending, _ := fixture.jobs.ClaimDueJobs(ctx, []string{bluememo.JobKindExtract, bluememo.JobKindProfile}, fixture.now.Add(time.Hour), time.Minute, 10); len(pending) != 0 {
		t.Fatalf("expected no jobs left, got %+v", pending)
	}
}
