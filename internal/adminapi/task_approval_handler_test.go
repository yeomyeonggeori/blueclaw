package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

func approvalTestTaskRunService(t *testing.T) (*task.TaskRunService, *task.TaskEventService) {
	t.Helper()
	taskEventService := task.NewTaskEventService()
	return task.NewTaskRunService(taskEventService), taskEventService
}

func configuredApprovalHandler(taskRunService *task.TaskRunService) TaskApprovalHandler {
	return TaskApprovalHandler{
		TaskRunService: taskRunService,
		TaskLauncher:   agentruntime.NewTaskLauncher(nil, taskRunService, agentruntime.NewToolCatalogBuilder()),
	}
}

func postApproval(t *testing.T, handler TaskApprovalHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	responseRecorder := httptest.NewRecorder()
	handler.HandleApproveTaskRun(responseRecorder, httptest.NewRequest(http.MethodPost, "/admin/api/run/approve", bytes.NewBufferString(body)))
	return responseRecorder
}

func TestApprovalRefusesATaskRunThatIsNotWaitingForApproval(t *testing.T) {
	taskRunService, _ := approvalTestTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "캘린더 정리")
	handler := configuredApprovalHandler(taskRunService)

	responseRecorder := postApproval(t, handler, `{"taskRunID":"`+taskRun.TaskRunID+`","decision":"approve"}`)
	if responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected a running task run to be unapprovable, got %d", responseRecorder.Code)
	}
	if !strings.Contains(responseRecorder.Body.String(), "waiting for approval") {
		t.Fatalf("expected the refusal to say why, got %s", responseRecorder.Body.String())
	}
}

func TestApprovalRefusesAnUnknownTaskRunAndAnUnknownDecision(t *testing.T) {
	taskRunService, _ := approvalTestTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "캘린더 정리")
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatalf("expected the run to pause: %v", errorValue)
	}
	handler := configuredApprovalHandler(taskRunService)

	if responseRecorder := postApproval(t, handler, `{"taskRunID":"missing","decision":"approve"}`); responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected an unknown task run to be refused, got %d", responseRecorder.Code)
	}
	responseRecorder := postApproval(t, handler, `{"taskRunID":"`+taskRun.TaskRunID+`","decision":"looks good"}`)
	if responseRecorder.Code != http.StatusBadRequest || !strings.Contains(responseRecorder.Body.String(), "reject") {
		t.Fatalf("expected a free-text decision to be refused with the allowed set, got %d %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestApprovalRefusesWhenNoLauncherCanCarryTheDecision(t *testing.T) {
	taskRunService, _ := approvalTestTaskRunService(t)
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "캘린더 정리")
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatalf("expected the run to pause: %v", errorValue)
	}
	handler := TaskApprovalHandler{TaskRunService: taskRunService}

	responseRecorder := postApproval(t, handler, `{"taskRunID":"`+taskRun.TaskRunID+`","decision":"approve"}`)
	if responseRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected an unconfigured approval path to refuse rather than silently accept, got %d", responseRecorder.Code)
	}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRun.TaskRunID) {
		if taskEvent.Name == "approval.decided" {
			t.Fatal("expected no decision to be recorded when the approval could not be carried out")
		}
	}
}

func TestApprovalDecisionMapsToTheApprovalSignalTheGateExpects(t *testing.T) {
	for decision, expectedSignal := range map[string]string{"approve": "approve", "reject": "reject"} {
		approvalSignal, errorValue := approvalSignalOf(decision)
		if errorValue != nil {
			t.Fatalf("expected %q to map: %v", decision, errorValue)
		}
		if string(approvalSignal) != expectedSignal {
			t.Fatalf("expected %q to carry the %q signal, got %q", decision, expectedSignal, approvalSignal)
		}
	}
}

func TestApprovalRequestRejectsAMalformedBody(t *testing.T) {
	taskRunService, _ := approvalTestTaskRunService(t)
	handler := configuredApprovalHandler(taskRunService)
	if responseRecorder := postApproval(t, handler, `not json`); responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected a malformed body to be refused, got %d", responseRecorder.Code)
	}
	var decoded map[string]string
	responseRecorder := postApproval(t, handler, `{}`)
	if json.Unmarshal(responseRecorder.Body.Bytes(), &decoded) != nil || decoded["error"] == "" {
		t.Fatalf("expected a JSON error body, got %s", responseRecorder.Body.String())
	}
}

func TestApprovingAHeldCallDecidesTheHoldAndResumesTheRunWithoutCarryingTheCallOut(t *testing.T) {
	taskRunService, _ := approvalTestTaskRunService(t)
	harness := harnesstest.New(taskRunService)
	handler := TaskApprovalHandler{
		TaskRunService: taskRunService,
		TaskLauncher:   agentruntime.NewTaskLauncher(harness, taskRunService, agentruntime.NewToolCatalogBuilder()),
	}
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "일정 삭제")
	holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventHint":"event-1"}`), Confirmation: "삭제할까요?"}, nil)
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}

	responseRecorder := postApproval(t, handler, `{"taskRunID":"`+taskRun.TaskRunID+`","decision":"approve"}`)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected the approval to resume the run, got %d %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(holds) != 1 || holds[0].State != holdrecord.StateApproved {
		t.Fatalf("expected the hold approved and left for the harness's repeated call to spend, got %+v", holds)
	}
	request := harness.LastTurnRequest()
	if !request.IsRuntimeRestartResume || request.ExistingTaskRunID != taskRun.TaskRunID || len(request.CarriedOutCalls) != 0 {
		t.Fatalf("expected a restart resume of the held run that carries nothing out, got %+v", request)
	}
}

type recordedLiveHolds struct {
	isAwaited bool
	answers   []agentcontract.ApprovalSignal
}

func (holds *recordedLiveHolds) AnswerAwaitedHold(_ string, signal agentcontract.ApprovalSignal) (bool, error) {
	holds.answers = append(holds.answers, signal)
	return holds.isAwaited, nil
}

func heldRunWithAdminHandler(t *testing.T, liveHolds LiveHolds) (TaskApprovalHandler, *harnesstest.Harness, task.TaskRun, *task.TaskRunService) {
	t.Helper()
	taskRunService, _ := approvalTestTaskRunService(t)
	harness := harnesstest.New(taskRunService)
	handler := TaskApprovalHandler{
		LiveHolds:      liveHolds,
		TaskRunService: taskRunService,
		TaskLauncher:   agentruntime.NewTaskLauncher(harness, taskRunService, agentruntime.NewToolCatalogBuilder()),
	}
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "일정 삭제")
	holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventHint":"event-1"}`), Confirmation: "삭제할까요?"}, nil)
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}
	return handler, harness, taskRun, taskRunService
}

func TestAnApprovalWhileTheAskingTurnIsStillWaitingIsHandedToThatTurnAndNothingIsResumed(t *testing.T) {
	liveHolds := &recordedLiveHolds{isAwaited: true}
	handler, harness, taskRun, taskRunService := heldRunWithAdminHandler(t, liveHolds)

	responseRecorder := postApproval(t, handler, `{"taskRunID":"`+taskRun.TaskRunID+`","decision":"approve"}`)

	if responseRecorder.Code != http.StatusOK || len(liveHolds.answers) != 1 || liveHolds.answers[0] != agentcontract.ApprovalSignalApprove {
		t.Fatalf("expected the live waiter to be answered once, got %d %+v", responseRecorder.Code, liveHolds.answers)
	}
	if harness.RunTurnCallCount() != 0 {
		t.Fatal("a second turn was launched beside the one still waiting on the hold")
	}
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].State != holdrecord.StatePending {
		t.Fatalf("the waiting turn settles the hold itself, got %+v", holds)
	}
}

func TestAnApprovalWithNoTurnWaitingOnTheHoldResumesTheRunAsARestartResume(t *testing.T) {
	liveHolds := &recordedLiveHolds{isAwaited: false}
	handler, harness, taskRun, _ := heldRunWithAdminHandler(t, liveHolds)

	responseRecorder := postApproval(t, handler, `{"taskRunID":"`+taskRun.TaskRunID+`","decision":"approve"}`)

	if responseRecorder.Code != http.StatusOK || !harness.LastTurnRequest().IsRuntimeRestartResume {
		t.Fatalf("expected the run resumed as a restart resume, got %d %+v", responseRecorder.Code, harness.LastTurnRequest())
	}
}
