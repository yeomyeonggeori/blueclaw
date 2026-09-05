package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

func TestTaskLauncherPersistsTimedPreRouterRecordsOnSuccess(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseTurnRouter(&clockRecordingTurnRouter{})

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "오늘 무슨 요일이야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	assertTimedLaunchRecords(t, taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID))
}

func TestTaskLauncherPersistsTimedPreRouterRecordsOnFailure(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseTurnRouter(&failingTurnRouter{errorValue: errors.New("router unavailable")})
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, authoredRuntimeFailureLanguageModel{reply: "요청을 처리하지 못했습니다."}))

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "오늘 무슨 요일이야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	assertTimedLaunchRecords(t, taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID))
}

func assertTimedLaunchRecords(t *testing.T, taskEvents []task.TaskEvent) {
	t.Helper()
	expectedStepNames := map[string]bool{
		"resolve_requester_email":        false,
		"resolve_active_circle":          false,
		"conversation_artifact_manifest": false,
		"build_router_tool_set":          false,
		"router_call":                    false,
	}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "agent.launch_step.result" && taskEvent.Name != "agent.launch_step.error" {
			continue
		}
		var record launchStepRecord
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &record); errorValue != nil {
			t.Fatalf("expected launch step record: %v", errorValue)
		}
		if _, isExpected := expectedStepNames[record.StepName]; !isExpected {
			continue
		}
		expectedStepNames[record.StepName] = true
		if record.StartedAtUnixMs == 0 || record.DurationMs < 0 {
			t.Fatalf("expected timing evidence for %s, got %+v", record.StepName, record)
		}
	}
	for stepName, found := range expectedStepNames {
		if !found {
			t.Fatalf("expected timed launch step %s, got %+v", stepName, taskEvents)
		}
	}
}

type failingTurnRouter struct {
	errorValue error
}

func (router *failingTurnRouter) Plan(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	return agentcontract.TurnDecision{}, router.errorValue
}

func (router *failingTurnRouter) PlanObserved(context.Context, agentcontract.AgentRequest, *agentcontract.TurnRouterCallLedger) (agentcontract.TurnDecision, error) {
	return agentcontract.TurnDecision{}, router.errorValue
}

func TestTaskLauncherSetsExecutionStartAfterRouting(t *testing.T) {
	turnStartedAt := time.Now().Add(-time.Second)
	router := &clockRecordingTurnRouter{}
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	taskLauncher := NewTaskLauncher(harness, taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseTurnRouter(router)

	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "작업을 시작해",
		TurnStartedAt:     turnStartedAt,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if router.routed.TurnStartedAt != turnStartedAt {
		t.Fatalf("expected turn start to remain unchanged, got %s", router.routed.TurnStartedAt)
	}
	turnRequest := harness.LastTurnRequest()
	if !turnRequest.ExecutionStartedAt.After(turnStartedAt) {
		t.Fatalf("expected execution start after routing, got %s", turnRequest.ExecutionStartedAt)
	}
	restartExecutionStartedAt := time.Now().Add(-time.Minute)
	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:                 TaskLaunchSourceConnector,
		RequesterPersonID:      "person-1",
		ConversationID:         "conversation-1",
		Prompt:                 "작업을 재개해",
		TurnStartedAt:          turnStartedAt,
		ExecutionStartedAt:     restartExecutionStartedAt,
		IsRuntimeRestartResume: true,
		PersonAccess:           policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnRequest = harness.LastTurnRequest()
	if turnRequest.ExecutionStartedAt != restartExecutionStartedAt {
		t.Fatalf("expected restart execution start to remain unchanged, got %s", turnRequest.ExecutionStartedAt)
	}
}
