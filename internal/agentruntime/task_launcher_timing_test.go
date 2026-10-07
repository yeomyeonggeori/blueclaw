package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
)

func TestTaskLauncherSetsExecutionStartWhenTheTurnLaunches(t *testing.T) {
	turnStartedAt := time.Now().Add(-time.Second)
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	taskLauncher := NewTaskLauncher(harness, taskRunService, NewToolCatalogBuilder())

	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "작업을 시작해",
		TurnStartedAt:     turnStartedAt,
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnRequest := harness.LastTurnRequest()
	if !turnRequest.ExecutionStartedAt.After(turnStartedAt) {
		t.Fatalf("expected execution start after the turn start, got %s", turnRequest.ExecutionStartedAt)
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
		PersonAccess:           policy.PersonAccess{PersonID: "person-1"},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	turnRequest = harness.LastTurnRequest()
	if turnRequest.ExecutionStartedAt != restartExecutionStartedAt {
		t.Fatalf("expected restart execution start to remain unchanged, got %s", turnRequest.ExecutionStartedAt)
	}
}

func TestARunWaitingOnApprovalIsNotFailedWhenItsClientLeavesMidTurn(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())
	taskRun := taskRunService.CreateTaskRun("person-1", "보낼까요?", "default")
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, "보낼까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}
	clientLeft, leave := context.WithCancel(context.Background())
	leave()

	heldRun, isLeftHeld := taskLauncher.runLeftHeldByItsClient(clientLeft, launchStepRecord{Error: "peer connection closed"}, taskRun.TaskRunID)
	_, isLeftHeldWithClientPresent := taskLauncher.runLeftHeldByItsClient(context.Background(), launchStepRecord{Error: "peer connection closed"}, taskRun.TaskRunID)

	if !isLeftHeld || heldRun.TaskRunID != taskRun.TaskRunID {
		t.Fatalf("a run waiting on approval whose client left was treated as failed: %+v", heldRun)
	}
	if isLeftHeldWithClientPresent {
		t.Fatal("a turn that failed while its client was still there was treated as held")
	}
}

var timedLaunchStepNames = []string{
	"resolve_requester_email",
	"resolve_active_circle",
	"conversation_artifact_manifest",
	"provision_requester_workspace",
	"build_tool_set",
	"audit_tool_registry",
	"load_memory",
	"carry_out_approved_call",
	"run_turn",
}

func TestTaskLauncherPersistsTimedLaunchRecordsOnSuccess(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "오늘 무슨 요일이야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	assertTimedLaunchRecords(t, taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID), "agent.launch_step.result")
}

func TestTaskLauncherPersistsTimedLaunchRecordsOnFailure(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskLauncher := NewTaskLauncher(turnFailingHarness{Harness: harnesstest.New(taskRunService), failure: errors.New("agent unavailable")}, taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "오늘 무슨 요일이야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	assertTimedLaunchRecords(t, taskEvents, "agent.launch_step.result", "agent.launch_step.error")
	if runTurn := launchStepRecordNamed(t, taskEvents, "run_turn"); runTurn.Status != launchStepStatusError || runTurn.Error != "agent unavailable" {
		t.Fatalf("the failed turn must be recorded with its error, got %+v", runTurn)
	}
}

func TestTheAgentThatPlansTheTurnIsToldWhatDayItIs(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	taskLauncher := NewTaskLauncher(harness, taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseCompanyProvider(func() agentcontract.CompanyContext {
		return agentcontract.CompanyContext{Name: "여명거리", TimeZone: "Asia/Seoul"}
	})

	if _, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "금요일에 휴가 쓸게",
		ResponseLanguage:  "ko",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	}); errorValue != nil {
		t.Fatal(errorValue)
	}

	turnRequest := harness.LastTurnRequest()
	if turnRequest.EnvironmentNow.IsZero() {
		t.Fatal("an agent that is not told the date resolves 금요일 against nothing")
	}
	if turnRequest.Company.TimeZone != "Asia/Seoul" {
		t.Fatalf("the agent reads the clock in the company's zone, got %q", turnRequest.Company.TimeZone)
	}
	if !turnRequest.EnvironmentNow.Equal(turnRequest.TurnStartedAt) {
		t.Fatalf("the turn and the environment disagree about now: %s and %s", turnRequest.TurnStartedAt, turnRequest.EnvironmentNow)
	}
}

type turnFailingHarness struct {
	*harnesstest.Harness
	failure error
}

func (harness turnFailingHarness) RunTurn(context.Context, agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	return agentcontract.AgentTurnResult{}, harness.failure
}

func launchStepRecordNamed(t *testing.T, taskEvents []task.TaskEvent, stepName string) launchStepRecord {
	t.Helper()
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != "agent.launch_step.result" && taskEvent.Name != "agent.launch_step.error" {
			continue
		}
		var record launchStepRecord
		if errorValue := json.Unmarshal([]byte(taskEvent.Body), &record); errorValue != nil {
			t.Fatalf("expected a launch step record: %v", errorValue)
		}
		if record.StepName == stepName {
			return record
		}
	}
	t.Fatalf("expected the launch step %s to be recorded, got %+v", stepName, taskEvents)
	return launchStepRecord{}
}

func assertTimedLaunchRecords(t *testing.T, taskEvents []task.TaskEvent, eventNames ...string) {
	t.Helper()
	for _, stepName := range timedLaunchStepNames {
		record := launchStepRecordNamed(t, taskEvents, stepName)
		if record.StartedAtUnixMs == 0 || record.DurationMs < 0 {
			t.Fatalf("expected timing evidence for %s, got %+v", stepName, record)
		}
	}
	for _, eventName := range eventNames {
		if !slices.ContainsFunc(taskEvents, func(taskEvent task.TaskEvent) bool { return taskEvent.Name == eventName }) {
			t.Fatalf("expected a %s event, got %+v", eventName, taskEvents)
		}
	}
}
