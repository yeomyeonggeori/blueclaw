package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
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
