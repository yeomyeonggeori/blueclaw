package agentruntime

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func approvalContinuationLauncher(t *testing.T, taskRunService *task.TaskRunService) (*TaskLauncher, *harnesstest.Harness) {
	t.Helper()
	harness := harnesstest.New(taskRunService)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(seededMemoryStore(t, "person-1", "The user leads the quarterly launch project."), nil)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{"default": {"memory_search"}}, nil)
	return NewTaskLauncher(harness, taskRunService, toolCatalogBuilder), harness
}

func launchApprovalContinuation(t *testing.T, taskLauncher *TaskLauncher, taskRunID string) {
	t.Helper()
	_, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:                    TaskLaunchSourceAdmin,
		SourceReference:           "terminal:" + taskRunID,
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "지난 분기 뭐였는지 찾아줘",
		IsApprovalContinuation:    true,
		ExistingTaskRunID:         taskRunID,
		PersonAccess:              policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		AccessibleConversationIDs: []string{"channel-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected the approval continuation to launch: %v", errorValue)
	}
}

func taskRunAwaitingApprovalOf(t *testing.T, taskRunService *task.TaskRunService, decision string) string {
	t.Helper()
	taskRun := taskRunService.CreateTaskRun("person-1", "channel-1", "지난 분기 뭐였는지 찾아줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "approval.pending_call", `{"toolName":"memory_search","toolInput":{"query":"quarterly launch"},"confirmation":"기억을 찾아볼까요?"}`)
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "approval.decided", `{"decision":"`+decision+`"}`)
	return taskRun.TaskRunID
}

func TestTheHostCarriesOutTheApprovedCallAndHandsTheResultToTheHarness(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskLauncher, harness := approvalContinuationLauncher(t, taskRunService)
	taskRunID := taskRunAwaitingApprovalOf(t, taskRunService, "confirm")

	launchApprovalContinuation(t, taskLauncher, taskRunID)

	carriedOutCalls := harness.LastTurnRequest().CarriedOutCalls
	if len(carriedOutCalls) != 1 || carriedOutCalls[0].ToolName != "memory_search" {
		t.Fatalf("expected the approved call to reach the harness as carried out, got %+v", carriedOutCalls)
	}
	if carriedOutCalls[0].Result.Failed() {
		t.Fatalf("expected the carried out call to have run, got %+v", carriedOutCalls[0].Result)
	}
	if !containsTaskEvent(taskRunService.ListTaskEvent(taskRunID), "approval.executed") {
		t.Fatal("expected the ledger to record that the held call ran")
	}
}

func TestADeclinedCallIsNotCarriedOut(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskLauncher, harness := approvalContinuationLauncher(t, taskRunService)
	taskRunID := taskRunAwaitingApprovalOf(t, taskRunService, "cancel")

	launchApprovalContinuation(t, taskLauncher, taskRunID)

	if carriedOutCalls := harness.LastTurnRequest().CarriedOutCalls; len(carriedOutCalls) != 0 {
		t.Fatalf("expected a declined call to stay uncarried, got %+v", carriedOutCalls)
	}
	if containsTaskEvent(taskRunService.ListTaskEvent(taskRunID), "approval.executed") {
		t.Fatal("expected no execution record for a call the requester declined")
	}
}

func TestACallIsCarriedOutOnceAndNotAgainOnTheNextResume(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskLauncher, harness := approvalContinuationLauncher(t, taskRunService)
	harness.TurnStatus = taskstate.TaskStatusWaitingApproval
	taskRunID := taskRunAwaitingApprovalOf(t, taskRunService, "confirm")

	launchApprovalContinuation(t, taskLauncher, taskRunID)
	launchApprovalContinuation(t, taskLauncher, taskRunID)

	if carriedOutCalls := harness.LastTurnRequest().CarriedOutCalls; len(carriedOutCalls) != 0 {
		t.Fatalf("expected the second resume to carry out nothing, got %+v", carriedOutCalls)
	}
}
