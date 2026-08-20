package approvalgate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func gateFixture(t *testing.T) (*Gate, *task.TaskRunService, task.TaskRun) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	return New(taskRunService), taskRunService, taskRun
}

func approvalRequestFixture(taskRunID string) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		TaskRunID:         taskRunID,
		ToolName:          "calendar_delete",
		ToolInput:         json.RawMessage(`{"eventID":"event-1"}`),
		ApprovalScope:     "calendar",
		HarnessSession:    mcpserver.HarnessSession{HarnessName: "claude-code", SessionID: "session-uuid", IsResumable: true},
	}
}

func heldCallEventBody(t *testing.T, taskRunService *task.TaskRunService, taskRunID string) string {
	t.Helper()
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "approval.pending_call" {
			return taskEvent.Body
		}
	}
	t.Fatal("expected a held call to be recorded")
	return ""
}

func TestAHeldCallIsRecordedWithTheConversationItWasHeldIn(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("expected the call to be held, got %+v", outcome)
	}

	body := heldCallEventBody(t, taskRunService, taskRun.TaskRunID)
	for _, expectedFragment := range []string{"calendar_delete", "event-1", "calendar", "claude-code", "session-uuid"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("expected the held call to carry %q so it can be resumed, got %s", expectedFragment, body)
		}
	}
	pausedTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if pausedTaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected the task run to wait for the requester, got %q", pausedTaskRun.Status)
	}
}

func TestACallWithNoTaskRunToAnswerOnIsUnanswerableRatherThanHeld(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	gate := New(taskRunService)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(""))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionUnanswerable {
		t.Fatalf("a call nobody can be asked about is not waiting for an answer, got %+v", outcome)
	}
}

func recordDecision(taskRunService *task.TaskRunService, taskRunID string, decision string) {
	taskRunService.AppendTaskEvent(taskRunID, "approval.decided", `{"decision":"`+decision+`"}`)
}

func TestTheSameCallRunsOnceTheRequesterHasApprovedIt(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	heldOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if heldOutcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("expected the first call to be held, got %+v", heldOutcome)
	}

	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")

	approvedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if approvedOutcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected the approved call to run when the agent reissues it, got %+v", approvedOutcome)
	}
}

func TestAnApprovalIsSpentOnTheCallItAnsweredAndNotTheNextOne(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	repeatedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if repeatedOutcome.Decision == mcpserver.ApprovalDecisionApproved {
		t.Fatal("expected one approval to authorise one call, so a second identical call is asked about again")
	}
}

func approvalRequestForEvent(taskRunID string, toolInput string) mcpserver.ApprovalRequest {
	approvalRequest := approvalRequestFixture(taskRunID)
	approvalRequest.ToolInput = json.RawMessage(toolInput)
	return approvalRequest
}

func TestAnApprovalDoesNotCarryOverToACallTheRequesterNeverSaw(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestForEvent(taskRun.TaskRunID, `{"eventID":"event-1"}`))
	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")

	substitutedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestForEvent(taskRun.TaskRunID, `{"eventID":"event-2"}`))
	if substitutedOutcome.Decision == mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected approving one call to authorise that call alone, so a substituted target is asked about again, got %+v", substitutedOutcome)
	}
}

func TestAnApprovedCallIsStillRecognisedWhenTheAgentReordersItsInput(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestForEvent(taskRun.TaskRunID, `{"eventID":"event-1","calendarID":"team"}`))
	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")

	reorderedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestForEvent(taskRun.TaskRunID, `{"calendarID":"team","eventID":"event-1"}`))
	if reorderedOutcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected the same call to be recognised through a reordered input rather than asked about twice, got %+v", reorderedOutcome)
	}
}

func TestADeclinedCallComesBackRejectedRatherThanHeldForever(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	recordDecision(taskRunService, taskRun.TaskRunID, "cancel")

	declinedOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if declinedOutcome.Decision != mcpserver.ApprovalDecisionRejected {
		t.Fatalf("expected a declined call to be told so, got %+v", declinedOutcome)
	}
}

func heldCallEventBodyNamed(t *testing.T, taskRunService *task.TaskRunService, taskRunID string, eventName string) string {
	t.Helper()
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == eventName {
			return taskEvent.Body
		}
	}
	t.Fatalf("expected a %s event to be recorded, got %+v", eventName, taskRunService.ListTaskEvent(taskRunID))
	return ""
}

func TestAHeldCallTellsTheRequesterWhatTheyAreBeingAskedAbout(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	approvalRequest := approvalRequestFixture(taskRun.TaskRunID)
	approvalRequest.ResponseLanguage = "ko"
	approvalRequest.SideEffectClass = "external_send"

	gate.AwaitApproval(context.Background(), approvalRequest)

	confirmationBody := heldCallEventBodyNamed(t, taskRunService, taskRun.TaskRunID, "confirmation.requested")
	for _, expectedFragment := range []string{"userFacingMessage", "responseLanguage", "external_send"} {
		if !strings.Contains(confirmationBody, expectedFragment) {
			t.Fatalf("the connector reads this event to ask the requester, expected %q in %s", expectedFragment, confirmationBody)
		}
	}
}

func TestAScopedHeldCallOffersApprovingTheWholeTask(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)

	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	askBody := heldCallEventBodyNamed(t, taskRunService, taskRun.TaskRunID, "ask.requested")
	for _, expectedFragment := range []string{`"approvalScope":"calendar"`, `"sessionApprovable":true`, "confirm_task"} {
		if !strings.Contains(askBody, expectedFragment) {
			t.Fatalf("confirm_task is resolved by reading the scope off this event, expected %q in %s", expectedFragment, askBody)
		}
	}
}

func TestAnUnscopedHeldCallDoesNotOfferAScopeItHasNot(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	approvalRequest := approvalRequestFixture(taskRun.TaskRunID)
	approvalRequest.ApprovalScope = ""

	gate.AwaitApproval(context.Background(), approvalRequest)

	if askBody := heldCallEventBodyNamed(t, taskRunService, taskRun.TaskRunID, "ask.requested"); strings.Contains(askBody, "sessionApprovable") {
		t.Fatalf("a call with no approval scope must not offer approving the whole task, got %s", askBody)
	}
}

func TestApprovingTheWholeTaskLetsTheNextCallInThatScopeRun(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestForEvent(taskRun.TaskRunID, `{"eventID":"event-1"}`))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "approval.scope_granted", `{"scope":"calendar"}`)

	nextCallOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestForEvent(taskRun.TaskRunID, `{"eventID":"event-2"}`))
	if nextCallOutcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("approving the whole task means the requester is not asked again inside that scope, got %+v", nextCallOutcome)
	}
}

func TestApprovingOneScopeDoesNotApproveAnother(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "approval.scope_granted", `{"scope":"messaging"}`)

	heldOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if heldOutcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a grant covers the scope it was given for and no other, got %+v", heldOutcome)
	}
}

type wordingLanguageModel struct {
	question    string
	failure     error
	lastRequest model.StructuredResponseRequest
}

func (languageModel *wordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("the approval gate only asks for structured output")
}

func (languageModel *wordingLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.lastRequest = request
	if languageModel.failure != nil {
		return model.StructuredResponse{}, languageModel.failure
	}
	return model.StructuredResponse{Content: `{"question":"` + languageModel.question + `"}`}, nil
}

func TestTheRequesterIsAskedInWordsTheModelChose(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	languageModel := &wordingLanguageModel{question: "내일 팀 회의를 캘린더에서 지울까요?"}
	gate.UseLanguageModel(languageModel)

	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if !strings.Contains(heldCallEventBodyNamed(t, taskRunService, taskRun.TaskRunID, "confirmation.requested"), "내일 팀 회의를 캘린더에서 지울까요?") {
		t.Fatal("the requester has to be asked in words a model wrote, not in a sentence assembled from a tool name")
	}
	if !strings.Contains(marshalRequestMessages(languageModel.lastRequest), "calendar_delete") {
		t.Fatalf("the model needs the pending call to word the question, got %s", marshalRequestMessages(languageModel.lastRequest))
	}
}

func TestAnUnwordableCallStillReachesTheRequesterAsTheCallItself(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseLanguageModel(&wordingLanguageModel{failure: errors.New("llmd is unreachable")})

	heldOutcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if heldOutcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a call nobody could word still has to be held rather than run, got %+v", heldOutcome)
	}
	confirmationBody := heldCallEventBodyNamed(t, taskRunService, taskRun.TaskRunID, "confirmation.requested")
	for _, expectedFragment := range []string{"calendar_delete", "event-1"} {
		if !strings.Contains(confirmationBody, expectedFragment) {
			t.Fatalf("with no wording the requester gets the raw call, expected %q in %s", expectedFragment, confirmationBody)
		}
	}
}

func marshalRequestMessages(request model.StructuredResponseRequest) string {
	messages := []string{}
	for _, message := range request.Messages {
		messages = append(messages, message.Content)
	}
	return strings.Join(messages, "\n")
}

func taskEventNames(taskRunService *task.TaskRunService, taskRunID string) []string {
	names := []string{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		names = append(names, taskEvent.Name)
	}
	return names
}

func hasTaskEventNamed(taskRunService *task.TaskRunService, taskRunID string, eventName string) bool {
	for _, name := range taskEventNames(taskRunService, taskRunID) {
		if name == eventName {
			return true
		}
	}
	return false
}

func TestAHeldCallIsRecordedOnTheTaskRunTheCallIsRunningIn(t *testing.T) {
	gate, taskRunService, abandonedTaskRun := gateFixture(t)
	runningTaskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "다시 해봐")
	turnGate := gate.TurnGate(TurnContext{RequesterPersonID: "person-1"})

	invokeThroughGateInContext(t, toolcontract.WithTaskRunID(context.Background(), runningTaskRun.TaskRunID), turnGate, "file_delete")

	for _, expectedEventName := range []string{"approval.pending_call", "confirmation.requested", "ask.requested"} {
		if !hasTaskEventNamed(taskRunService, runningTaskRun.TaskRunID, expectedEventName) {
			t.Fatalf("expected %q on the run the call is executing in, got %+v", expectedEventName, taskEventNames(taskRunService, runningTaskRun.TaskRunID))
		}
		if hasTaskEventNamed(taskRunService, abandonedTaskRun.TaskRunID, expectedEventName) {
			t.Fatalf("expected %q never to reach the run the turn left behind, got %+v", expectedEventName, taskEventNames(taskRunService, abandonedTaskRun.TaskRunID))
		}
	}
	parkedTaskRun, _ := taskRunService.FindTaskRun(runningTaskRun.TaskRunID)
	if parkedTaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected the run the call is executing in to be the one parked, got %q", parkedTaskRun.Status)
	}
	untouchedTaskRun, _ := taskRunService.FindTaskRun(abandonedTaskRun.TaskRunID)
	if untouchedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatal("expected the run the turn left behind never to be parked, since nothing will ever deliver its question")
	}
}

func TestACallCarryingNoTaskRunParksNothingAtAll(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	turnGate := gate.TurnGate(TurnContext{RequesterPersonID: "person-1"})

	executed, result := invokeThroughGateInContext(t, context.Background(), turnGate, "file_delete")

	if len(*executed) != 0 || !result.Failed() {
		t.Fatalf("expected a call nobody can be asked about never to run, got executed=%+v result=%+v", *executed, result)
	}
	if hasTaskEventNamed(taskRunService, taskRun.TaskRunID, "approval.pending_call") {
		t.Fatalf("expected a call with no run of its own to reach no run at all, got %+v", taskEventNames(taskRunService, taskRun.TaskRunID))
	}
}

func TestARunThatCannotBeParkedIsNeverToldItWasAsked(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	if _, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "done"); errorValue != nil {
		t.Fatalf("expected the fixture run to complete: %v", errorValue)
	}

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("expected the gate to answer: %v", errorValue)
	}

	if outcome.Decision != mcpserver.ApprovalDecisionUnanswerable {
		t.Fatalf("a call nobody can be asked about is not waiting for an answer, got %+v", outcome)
	}
	for _, questionEventName := range []string{"approval.pending_call", "confirmation.requested", "ask.requested"} {
		if hasTaskEventNamed(taskRunService, taskRun.TaskRunID, questionEventName) {
			t.Fatalf("a question that was never asked leaves no record saying it was, got %+v", taskEventNames(taskRunService, taskRun.TaskRunID))
		}
	}
	unparkedTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if unparkedTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatalf("expected a run that could not be parked never to report waiting, got %q", unparkedTaskRun.Status)
	}
}
