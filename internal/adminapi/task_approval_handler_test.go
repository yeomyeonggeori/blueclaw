package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func approvalTestTaskRunService(t *testing.T) (*task.TaskRunService, *task.TaskEventService) {
	t.Helper()
	taskEventService := task.NewTaskEventService()
	return task.NewTaskRunService(taskEventService), taskEventService
}

const (
	requesterEmail = "requester@example.com"
	adminEmail     = "admin@example.com"
)

func rosterOfARequesterAndAnAdmin() *identity.IdentityService {
	return identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{requesterEmail: "person-1", adminEmail: "person-admin"},
	})
}

type gateSettledQuestions struct {
	gate    *approvalgate.Gate
	answers []agentcontract.ApprovalSignal
}

func questionsSettledByTheGate(taskRunService *task.TaskRunService) *gateSettledQuestions {
	return &gateSettledQuestions{gate: approvalgate.New(taskRunService)}
}

func (questions *gateSettledQuestions) AnswerParkedQuestionAsOperator(ctx context.Context, taskRunID string, signal agentcontract.ApprovalSignal) (approvalgate.AnswerSettlement, error) {
	questions.answers = append(questions.answers, signal)
	optionKey := approvalgate.RejectOptionKey
	if signal == agentcontract.ApprovalSignalApprove {
		optionKey = approvalgate.ApproveOptionKey
	}
	return questions.gate.SettleAnswer(ctx, mcpserver.ApprovalRequest{TaskRunID: taskRunID}, optionKey, "terminal")
}

func configuredApprovalHandler(taskRunService *task.TaskRunService) TaskApprovalHandler {
	return TaskApprovalHandler{
		ParkedQuestions: questionsSettledByTheGate(taskRunService),
		TaskRunService:  taskRunService,
		TaskLauncher:    agentruntime.NewTaskLauncher(nil, taskRunService, agentruntime.NewToolCatalogBuilder()),
		IdentityService: rosterOfARequesterAndAnAdmin(),
	}
}

func decisionBy(approverEmail string, taskRunID string, decision string) string {
	return `{"taskRunID":"` + taskRunID + `","decision":"` + decision + `","approverEmail":"` + approverEmail + `"}`
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

	responseRecorder := postApproval(t, handler, decisionBy(requesterEmail, taskRun.TaskRunID, "approve"))
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

	if responseRecorder := postApproval(t, handler, decisionBy(requesterEmail, "missing", "approve")); responseRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected an unknown task run to be refused, got %d", responseRecorder.Code)
	}
	responseRecorder := postApproval(t, handler, decisionBy(requesterEmail, taskRun.TaskRunID, "looks good"))
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
	handler := TaskApprovalHandler{TaskRunService: taskRunService, IdentityService: rosterOfARequesterAndAnAdmin()}

	responseRecorder := postApproval(t, handler, decisionBy(requesterEmail, taskRun.TaskRunID, "approve"))
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

func TestApprovingAHeldCallResumesTheRunHandedThatCallCarriedOut(t *testing.T) {
	taskRunService, _ := approvalTestTaskRunService(t)
	harness := harnesstest.New(taskRunService)
	handler := TaskApprovalHandler{
		ParkedQuestions: questionsSettledByTheGate(taskRunService),
		TaskRunService:  taskRunService,
		TaskLauncher:    agentruntime.NewTaskLauncher(harness, taskRunService, agentruntime.NewToolCatalogBuilder()),
		IdentityService: rosterOfARequesterAndAnAdmin(),
	}
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "일정 삭제")
	holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventHint":"event-1"}`), Confirmation: "삭제할까요?"}, nil)
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}

	responseRecorder := postApproval(t, handler, decisionBy(requesterEmail, taskRun.TaskRunID, "approve"))

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected the approval to resume the run, got %d %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	request := harness.LastTurnRequest()
	if !request.IsRuntimeRestartResume || request.ExistingTaskRunID != taskRun.TaskRunID {
		t.Fatalf("expected a restart resume of the held run, got %+v", request)
	}
	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(request.CarriedOutCalls) != 1 || request.CarriedOutCalls[0].ToolName != "event_delete" || request.CarriedOutCalls[0].HoldID != holds[0].ID {
		t.Fatalf("expected the approved call carried out before the turn and handed to it, got %+v", request.CarriedOutCalls)
	}
}

func heldRunWithAdminHandler(t *testing.T) (TaskApprovalHandler, *gateSettledQuestions, *harnesstest.Harness, task.TaskRun, *task.TaskRunService) {
	t.Helper()
	taskRunService, _ := approvalTestTaskRunService(t)
	harness := harnesstest.New(taskRunService)
	questions := questionsSettledByTheGate(taskRunService)
	handler := TaskApprovalHandler{
		ParkedQuestions: questions,
		TaskRunService:  taskRunService,
		TaskLauncher:    agentruntime.NewTaskLauncher(harness, taskRunService, agentruntime.NewToolCatalogBuilder()),
		IdentityService: rosterOfARequesterAndAnAdmin(),
	}
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "일정 삭제")
	holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventHint":"event-1"}`), Confirmation: "삭제할까요?"}, nil)
	if _, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatal(errorValue)
	}
	return handler, questions, harness, taskRun, taskRunService
}

func TestAnOperatorApprovalSettlesTheParkedQuestionAndResumesTheRunOnce(t *testing.T) {
	handler, questions, harness, taskRun, _ := heldRunWithAdminHandler(t)

	responseRecorder := postApproval(t, handler, decisionBy(requesterEmail, taskRun.TaskRunID, "approve"))

	if responseRecorder.Code != http.StatusOK || len(questions.answers) != 1 || questions.answers[0] != agentcontract.ApprovalSignalApprove {
		t.Fatalf("expected the parked question to be answered once, got %d %+v", responseRecorder.Code, questions.answers)
	}
	if harness.RunTurnCallCount() != 1 || !harness.LastTurnRequest().IsRuntimeRestartResume {
		t.Fatalf("expected the parked run resumed once, got %d turns %+v", harness.RunTurnCallCount(), harness.LastTurnRequest())
	}
}

func TestOnlyThePersonWhoAskedMayDecideTheirHeldCall(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		approverEmail string
	}{
		{"an admin who is not the requester", adminEmail},
		{"nobody named", ""},
		{"an address the roster does not know", "stranger@example.com"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler, questions, harness, taskRun, taskRunService := heldRunWithAdminHandler(t)

			for _, decision := range []string{"approve", "reject"} {
				responseRecorder := postApproval(t, handler, decisionBy(testCase.approverEmail, taskRun.TaskRunID, decision))
				if responseRecorder.Code != http.StatusForbidden {
					t.Fatalf("expected %s to be refused, got %d %s", decision, responseRecorder.Code, responseRecorder.Body.String())
				}
			}
			if len(questions.answers) != 0 || harness.RunTurnCallCount() != 0 {
				t.Fatalf("the refused decision reached the run: answers %+v, turns %d", questions.answers, harness.RunTurnCallCount())
			}
			if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); holds[0].State != holdrecord.StatePending {
				t.Fatalf("the hold was decided by someone other than its requester: %+v", holds)
			}
		})
	}
}

func TestTheRequesterDecidesTheirHeldCallWhateverCaseTheirAddressIsWrittenIn(t *testing.T) {
	handler, questions, _, taskRun, taskRunService := heldRunWithAdminHandler(t)

	responseRecorder := postApproval(t, handler, decisionBy(" Requester@Example.com ", taskRun.TaskRunID, "reject"))

	if responseRecorder.Code != http.StatusOK || len(questions.answers) != 1 || questions.answers[0] != agentcontract.ApprovalSignalReject {
		t.Fatalf("expected the requester's reject to settle the question, got %d %+v", responseRecorder.Code, questions.answers)
	}
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); holds[0].State != holdrecord.StateRejected {
		t.Fatalf("expected the hold rejected, got %+v", holds)
	}
}
