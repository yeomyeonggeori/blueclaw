package approvalgate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

func taskRunWithHeldCall(t *testing.T) (*task.TaskRunService, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventApprovalHoldOpened, `{"toolName":"event_delete","toolInput":{"eventHint":"내일 회의"},"confirmation":"지울까요?"}`)
	return taskRunService, taskRun.TaskRunID
}

func TestAnApprovedCallIsHandedBackWithTheInputItWasApprovedWith(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	SettleLatest(taskRunService, taskRunID, "approve", "test")

	approvedCall, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID))
	if !isApproved || approvedCall.ToolName != "event_delete" {
		t.Fatalf("expected the approved call to be found, got %+v", approvedCall)
	}
	if !strings.Contains(string(approvedCall.ToolInput), "내일 회의") {
		t.Fatalf("expected the input the requester approved, got %q", approvedCall.ToolInput)
	}
}

func TestACallThatAlreadyRanIsNotHandedBackAgain(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	SettleLatest(taskRunService, taskRunID, "approve", "test")
	RecordApprovalSpent(taskRunService, taskRunID, "event_delete", json.RawMessage(`{"eventHint":"내일 회의"}`))

	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a call that already ran to stay carried out")
	}
}

func TestANewHeldCallDoesNotInheritTheDecisionMadeAboutTheLastOne(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	SettleLatest(taskRunService, taskRunID, "approve", "test")
	RecordApprovalSpent(taskRunService, taskRunID, "event_delete", json.RawMessage(`{"eventHint":"내일 회의"}`))
	taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalHoldOpened, `{"toolName":"message_send","toolInput":{"message":"보냅니다"},"confirmation":"보낼까요?"}`)

	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a freshly held call to wait for its own decision")
	}
}

func TestADeclinedCallIsReportedAsDeclinedRatherThanLeftSilent(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	SettleLatest(taskRunService, taskRunID, "reject", "test")

	declinedCallNote := DeclinedCallNote(taskRunService.ListTaskEvent(taskRunID))
	if !strings.Contains(declinedCallNote, "declined") {
		t.Fatalf("expected the resumed turn to learn the requester said no, got %q", declinedCallNote)
	}
	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a declined call to stay uncarried")
	}
}

func TestATurnWithNothingPendingCarriesNoInstruction(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "안녕")

	if declinedCallNote := DeclinedCallNote(taskRunService.ListTaskEvent(taskRun.TaskRunID)); declinedCallNote != "" {
		t.Fatalf("expected an ordinary turn to be left alone, got %q", declinedCallNote)
	}
}

func TestEverySurfaceRecordsTheRequesterDecisionUnderOneName(t *testing.T) {
	testCases := []struct {
		approvalSignal   agentcontract.ApprovalSignal
		expectedDecision string
	}{
		{agentcontract.ApprovalSignalApprove, "approve"},
		{agentcontract.ApprovalSignalReject, "reject"},
	}
	for _, testCase := range testCases {
		t.Run(string(testCase.approvalSignal), func(t *testing.T) {
			taskRunService, taskRunID := taskRunWithHeldCall(t)

			SettleSignal(taskRunService, taskRunID, &testCase.approvalSignal, "chat_reply")

			taskEvents := taskRunService.ListTaskEvent(taskRunID)
			decidedEvent := taskEvents[len(taskEvents)-1]
			if decidedEvent.Name != "approval.decided" || !strings.Contains(decidedEvent.Body, `"decision":"`+testCase.expectedDecision+`"`) {
				t.Fatalf("expected %q recorded as %q, got %s %s", testCase.approvalSignal, testCase.expectedDecision, decidedEvent.Name, decidedEvent.Body)
			}
		})
	}
}

func TestAnUnclearReplyDecidesNothing(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)

	SettleSignal(taskRunService, taskRunID, nil, "chat_reply")

	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "approval.decided" {
			t.Fatalf("a reply the classifier could not read is not an answer, got %s", taskEvent.Body)
		}
	}
}

func eventNamesAfterDecision(t *testing.T, approvalSignal agentcontract.ApprovalSignal) []string {
	t.Helper()
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	heldEventCount := len(taskRunService.ListTaskEvent(taskRunID))
	SettleSignal(taskRunService, taskRunID, &approvalSignal, "chat_reply")
	names := []string{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID)[heldEventCount:] {
		names = append(names, taskEvent.Name)
	}
	return names
}

func TestAnApprovalWritesOnlyTheEventsItAlwaysDid(t *testing.T) {
	if names := eventNamesAfterDecision(t, agentcontract.ApprovalSignalApprove); strings.Join(names, ",") != "approval.decided" {
		t.Fatalf("expected the decision alone, got %v", names)
	}
	if names := eventNamesAfterDecision(t, agentcontract.ApprovalSignalReject); strings.Join(names, ",") != "approval.decided" {
		t.Fatalf("expected the decision alone, got %v", names)
	}
}

func ApprovedPendingCall(taskEvents []agentcontract.TaskEvent) (ApprovedCall, bool) {
	approvedHold, isApproved := holdrecord.LatestHold(holdrecord.Holds(taskEvents), holdrecord.StateApproved)
	if !isApproved {
		return ApprovedCall{}, false
	}
	return ApprovedCall{HoldID: approvedHold.ID, ToolName: approvedHold.Call.ToolName, ToolInput: approvedHold.Call.ApprovedInput()}, true
}
