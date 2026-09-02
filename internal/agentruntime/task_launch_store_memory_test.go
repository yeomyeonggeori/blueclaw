package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestTaskLauncherInjectsProfileAndRecallFromTheStoreAndQueuesExtraction(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	repository := memory.NewInMemoryRepository()
	store := &memory.Store{Facts: repository, Profiles: repository, Jobs: repository, Embedder: &memorytest.HashEmbedder{}, Now: func() time.Time { return now }}
	taskRunService.RegisterTaskRunTransitionObserver(memory.TaskRunTransitionObserver{Store: *store}.Observe)

	seed := memory.Episode{EpisodeID: "episode-seed", SourceKind: memory.EpisodeSourceKindImport, SourceID: "seed", RequesterPersonID: "person-1", Content: "seed", OccurredAt: now}
	launchFact := memory.Fact{FactID: "fact-launch", EpisodeID: "episode-seed", ScopeType: memory.ScopeTypeWorkspace, Kind: memory.FactKindFact, Content: "The quarterly launch project is led by 이샘플", ValidFrom: now}
	if errorValue := repository.SaveEpisode(context.Background(), memory.EpisodeWrite{Episode: seed, Facts: []memory.FactWrite{{Fact: launchFact, Embedding: memorytest.Embed(launchFact.Content)}}}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := repository.SaveProfile(context.Background(), memory.Profile{PersonID: "person-1", IdentityLines: []string{"이샘플 prefers terse release notes"}, CurrentLines: []string{"이샘플 is preparing the quarterly launch"}, BuiltAt: now}); errorValue != nil {
		t.Fatal(errorValue)
	}

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(store, &memory.Ingester{Store: *store, Model: memorytest.NewScriptedModel()})
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{"default": {"memory_search"}}, nil)

	launchResult, errorValue := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		SourceReference:           "mattermost:post-1",
		RequesterPersonID:         "person-1",
		RequesterName:             "이샘플",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Platform:                  "mattermost",
		Prompt:                    "How is the quarterly launch project going?",
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		MemoryNamespaces:          []memory.MemoryNamespace{memory.ConversationNamespace("channel-1", 3, []string{"finance"})},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if len(launchResult.MemoryFacts) != 3 {
		t.Fatalf("expected two profile lines and one recalled fact, got %+v", launchResult.MemoryFacts)
	}
	if launchResult.MemoryFacts[0].SourceKind != "profile" || !containsMemoryFactContent(launchResult.MemoryFacts, "quarterly launch project is led by") {
		t.Fatalf("expected the profile first and the recalled fact present, got %+v", launchResult.MemoryFacts)
	}

	events := taskRunService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	bodiesByName := map[string]string{}
	for _, event := range events {
		bodiesByName[event.Name] = event.Body
	}
	var recallBody map[string]any
	if errorValue := json.Unmarshal([]byte(bodiesByName["memory.recall_injected"]), &recallBody); errorValue != nil {
		t.Fatalf("expected a recall event, got %v", bodiesByName)
	}
	if recallBody["profileLineCount"] != float64(2) || recallBody["recalledCount"] != float64(1) || recallBody["mode"] != memory.SearchModeHybrid {
		t.Fatalf("expected the recall event to carry counts and mode, got %v", recallBody)
	}
	var extractionContext memory.ExtractionContext
	if errorValue := json.Unmarshal([]byte(bodiesByName["memory.extraction_context"]), &extractionContext); errorValue != nil {
		t.Fatalf("expected an extraction context event, got %v", bodiesByName)
	}
	if extractionContext.RequesterName != "이샘플" || extractionContext.SecurityLevelRank != 3 || strings.Join(extractionContext.RequiredClasses, ",") != "finance" || extractionContext.Platform != "mattermost" {
		t.Fatalf("expected the conversation label in the extraction context, got %+v", extractionContext)
	}

	jobs, _ := repository.ClaimDueJobs(context.Background(), []string{memory.JobKindExtract}, now, time.Minute, 10)
	if len(jobs) != 1 || jobs[0].SubjectID != launchResult.TurnResult.TaskRun.TaskRunID {
		t.Fatalf("expected one extraction job for the finished run, got %+v", jobs)
	}
}

func TestTaskLauncherRecallsOnlyWhatTheRequesterMayRead(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	repository := memory.NewInMemoryRepository()
	store := &memory.Store{Facts: repository, Profiles: repository, Jobs: repository, Embedder: &memorytest.HashEmbedder{}, Now: func() time.Time { return now }}
	seed := memory.Episode{EpisodeID: "episode-seed", SourceKind: memory.EpisodeSourceKindImport, SourceID: "seed", RequesterPersonID: "person-1", Content: "seed", OccurredAt: now}
	secret := memory.Fact{FactID: "fact-secret", EpisodeID: "episode-seed", ScopeType: memory.ScopeTypeWorkspace, Kind: memory.FactKindFact, Content: "The quarterly launch headcount plan is frozen", SecurityLevelRank: 5, ValidFrom: now}
	if errorValue := repository.SaveEpisode(context.Background(), memory.EpisodeWrite{Episode: seed, Facts: []memory.FactWrite{{Fact: secret, Embedding: memorytest.Embed(secret.Content)}}}); errorValue != nil {
		t.Fatal(errorValue)
	}
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(store, nil)
	launchResult, errorValue := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-2",
		RequesterPersonID: "person-2",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "What is the quarterly launch headcount plan?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-2", SecurityLevelRank: 1},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(launchResult.MemoryFacts) != 0 {
		t.Fatalf("expected a fact above the requester's clearance to stay hidden, got %+v", launchResult.MemoryFacts)
	}
}
