package approvalgate

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func taskRunWithHeldCall(t *testing.T) (*task.TaskRunService, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, taskstate.TaskEventApprovalPendingCall, `{"toolName":"event_delete","toolInput":{"eventHint":"내일 회의"},"confirmation":"지울까요?"}`)
	return taskRunService, taskRun.TaskRunID
}

func TestAnApprovedCallIsHandedBackWithTheInputItWasApprovedWith(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalDecided, `{"decision":"confirm"}`)

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
	taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalDecided, `{"decision":"confirm"}`)
	taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalExecuted, `{"toolName":"event_delete"}`)

	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a call that already ran to stay carried out")
	}
}

func TestANewHeldCallDoesNotInheritTheDecisionMadeAboutTheLastOne(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalDecided, `{"decision":"confirm"}`)
	taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalExecuted, `{"toolName":"event_delete"}`)
	taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalPendingCall, `{"toolName":"message_send","toolInput":{"message":"보냅니다"},"confirmation":"보낼까요?"}`)

	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRunID)); isApproved {
		t.Fatal("expected a freshly held call to wait for its own decision")
	}
}

func TestADeclinedCallIsReportedAsDeclinedRatherThanLeftSilent(t *testing.T) {
	taskRunService, taskRunID := taskRunWithHeldCall(t)
	taskRunService.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalDecided, `{"decision":"cancel"}`)

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
		{agentcontract.ApprovalSignalApprove, "confirm"},
		{agentcontract.ApprovalSignalApproveTask, "confirm_task"},
		{agentcontract.ApprovalSignalReject, "cancel"},
	}
	for _, testCase := range testCases {
		t.Run(string(testCase.approvalSignal), func(t *testing.T) {
			taskRunService, taskRunID := taskRunWithHeldCall(t)

			RecordRequesterDecision(taskRunService, taskRunID, &testCase.approvalSignal, "chat_reply")

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
	unclearSignal := agentcontract.ApprovalSignalUnclear

	RecordRequesterDecision(taskRunService, taskRunID, &unclearSignal, "chat_reply")
	RecordRequesterDecision(taskRunService, taskRunID, nil, "chat_reply")

	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "approval.decided" {
			t.Fatalf("a reply the classifier could not read is not an answer, got %s", taskEvent.Body)
		}
	}
}
