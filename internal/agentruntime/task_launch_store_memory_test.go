package agentruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
)

func TestTaskLauncherInjectsRecallFromTheFilesAndRemembersTheFinishedRun(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	stores := memorytest.Open(t)
	memorytest.Remember(t, stores, memory.CircleScope("member"), "The quarterly launch project is led by 이샘플")
	memorytest.Remember(t, stores, memory.PersonScope("person-1"), "이샘플 prefers terse release notes")
	taskRunService.RegisterTaskRunTransitionObserver(memory.TaskRunTransitionObserver{
		Stores:   stores,
		TaskRuns: taskRunService,
	}.Observe)

	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStores(stores, nil)
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
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", Circles: []string{"member"}},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected launch to succeed: %v", errorValue)
	}
	if !containsMemoryFactContent(launchResult.MemoryFacts, "quarterly launch project is led by") {
		t.Fatalf("expected the circle's memory recalled, got %+v", launchResult.MemoryFacts)
	}

	bodiesByName := map[string]string{}
	for _, event := range taskRunService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID) {
		bodiesByName[event.Name] = event.Body
	}
	var recallBody map[string]any
	if errorValue := json.Unmarshal([]byte(bodiesByName["memory.recall_injected"]), &recallBody); errorValue != nil {
		t.Fatalf("expected a recall event, got %v", bodiesByName)
	}
	if recallBody["recalledCount"] != float64(len(launchResult.MemoryFacts)) {
		t.Fatalf("expected the recall event to count what was injected, got %v", recallBody)
	}
	var extractionContext memory.ExtractionContext
	if errorValue := json.Unmarshal([]byte(bodiesByName["memory.extraction_context"]), &extractionContext); errorValue != nil {
		t.Fatalf("expected an extraction context event, got %v", bodiesByName)
	}
	if extractionContext.RequesterName != "이샘플" || extractionContext.Platform != "mattermost" {
		t.Fatalf("expected the requester and platform in the extraction context, got %+v", extractionContext)
	}

	taskRunID := launchResult.TurnResult.TaskRun.TaskRunID
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, event := range taskRunService.ListTaskEvent(taskRunID) {
			if event.Name == "memory.extraction_completed" {
				return
			}
			if event.Name == "memory.extraction_failed" {
				t.Fatalf("expected the finished run to be remembered, got %s", event.Body)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("expected the finished run to be remembered")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTaskLauncherRecallsNothingFromAFileItDoesNotOpen(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	stores := memorytest.Open(t)
	memorytest.Remember(t, stores, memory.CircleScope("leadership"), "The quarterly launch headcount plan is frozen")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStores(stores, nil)
	launchResult, errorValue := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		SourceReference:   "mattermost:post-2",
		RequesterPersonID: "person-2",
		ProfileName:       "default",
		ConversationID:    "channel-1",
		Prompt:            "What is the quarterly launch headcount plan?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-2", Circles: []string{"member"}},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(launchResult.MemoryFacts) != 0 {
		t.Fatalf("expected a circle the requester is not in to stay closed, got %+v", launchResult.MemoryFacts)
	}
}

func containsMemoryFactContent(facts []memory.MemoryFact, fragment string) bool {
	for _, fact := range facts {
		if strings.Contains(fact.Content, fragment) {
			return true
		}
	}
	return false
}

type staticContainedCircles map[string][]string

func (circles staticContainedCircles) ContainedCircles() map[string][]string {
	return circles
}

func TestTaskLauncherRecallsAContainedCirclesMemoryForTheContainingCircle(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	stores := memorytest.Open(t)
	memorytest.Remember(t, stores, memory.CircleScope("platform"), "The platform circle deploys on Thursdays")
	launch := func(circleIDs []string, contained staticContainedCircles) []memory.MemoryFact {
		t.Helper()
		toolCatalogBuilder := NewToolCatalogBuilder()
		toolCatalogBuilder.UseMemoryStores(stores, contained)
		launchResult, errorValue := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder).Launch(context.Background(), TaskLaunchRequest{
			Source:            TaskLaunchSourceConnector,
			SourceReference:   "mattermost:post-" + strings.Join(circleIDs, "-"),
			RequesterPersonID: "person-2",
			ProfileName:       "default",
			ConversationID:    "channel-1",
			Prompt:            "When does platform deploy on Thursdays?",
			PersonAccess:      policy.PersonAccess{PersonID: "person-2", Circles: circleIDs},
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		return launchResult.MemoryFacts
	}
	if facts := launch([]string{"engineering"}, staticContainedCircles{"engineering": {"platform"}}); !containsMemoryFactContent(facts, "deploys on Thursdays") {
		t.Fatalf("expected a member of the containing circle to recall the platform memory, got %+v", facts)
	}
	if facts := launch([]string{"engineering"}, staticContainedCircles{}); len(facts) != 0 {
		t.Fatalf("expected no recall without containment, got %+v", facts)
	}
	if facts := launch([]string{"sales"}, staticContainedCircles{"engineering": {"platform"}}); len(facts) != 0 {
		t.Fatalf("expected a stranger circle to recall nothing, got %+v", facts)
	}
}
