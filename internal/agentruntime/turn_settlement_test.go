package agentruntime

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type verdictOnlyHarness struct {
	verdict    agentcontract.AgentTurnResult
	duringTurn func(agentcontract.AgentTurnRequest)
}

func (harness verdictOnlyHarness) RunTurn(_ context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if harness.duringTurn != nil {
		harness.duringTurn(request)
	}
	return harness.verdict, nil
}

func launchWithHarness(t *testing.T, taskRunService *task.TaskRunService, harness agentcontract.Harness) agentcontract.AgentTurnResult {
	t.Helper()
	launchResult, errorValue := NewTaskLauncher(harness, taskRunService, NewToolCatalogBuilder()).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "channel-1",
		Prompt:            "회의록 정리해줘",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return launchResult.TurnResult
}

func TestTheHostSettlesItsRunWithTheVerdictAnyHarnessReports(t *testing.T) {
	for _, verdict := range []agentcontract.TaskRun{
		{Status: agentcontract.TaskStatusCompleted, Result: "노트를 남겼습니다"},
		{Status: agentcontract.TaskStatusFailed, FailureReason: "the model gave up"},
		{Status: agentcontract.TaskStatusBlocked, FailureReason: "the calendar is unreachable"},
	} {
		t.Run(string(verdict.Status), func(t *testing.T) {
			taskRunService := task.NewTaskRunService(task.NewTaskEventService())

			turnResult := launchWithHarness(t, taskRunService, verdictOnlyHarness{verdict: agentcontract.AgentTurnResult{TaskRun: verdict, FinishMessage: "끝났습니다"}})

			storedTaskRun, _ := taskRunService.FindTaskRun(turnResult.TaskRun.TaskRunID)
			if storedTaskRun.Status != verdict.Status || storedTaskRun.Result != verdict.Result || storedTaskRun.FailureReason != verdict.FailureReason {
				t.Fatalf("the harness's verdict lands on the host's run, got %+v", storedTaskRun)
			}
			if turnResult.TaskRun.Status != verdict.Status || turnResult.FinishMessage != "끝났습니다" {
				t.Fatalf("the launch returns the settled run with the harness's reply, got %+v", turnResult)
			}
		})
	}
}

func TestEveryHarnessRunsItsTurnOnARunTheHostStarted(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	statusDuringTurn := agentcontract.TaskStatus("")

	launchWithHarness(t, taskRunService, verdictOnlyHarness{duringTurn: func(request agentcontract.AgentTurnRequest) {
		taskRun, _ := taskRunService.FindTaskRun(request.ExistingTaskRunID)
		statusDuringTurn = taskRun.Status
	}})

	if statusDuringTurn != agentcontract.TaskStatusRunning {
		t.Fatalf("a harness works on a running run, got %q", statusDuringTurn)
	}
}

func TestARunAwaitingAnAnswerWhenTheTurnEndsStaysAwaitingWhateverTheHarnessReports(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())

	turnResult := launchWithHarness(t, taskRunService, verdictOnlyHarness{
		verdict: agentcontract.AgentTurnResult{TaskRun: agentcontract.TaskRun{Status: agentcontract.TaskStatusCompleted}, FinishMessage: "보냈습니다"},
		duringTurn: func(request agentcontract.AgentTurnRequest) {
			taskRunService.PauseTaskRun(request.ExistingTaskRunID, agentcontract.TaskStatusWaitingApproval, "이 메시지를 보낼까요?")
		},
	})

	if turnResult.TaskRun.Status != agentcontract.TaskStatusWaitingApproval || turnResult.UserNotice != "이 메시지를 보낼까요?" {
		t.Fatalf("a run held for the requester's answer ends the turn asking it, got %q and %q", turnResult.TaskRun.Status, turnResult.UserNotice)
	}
}
