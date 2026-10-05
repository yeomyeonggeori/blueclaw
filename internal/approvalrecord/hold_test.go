package approvalrecord

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func openedHold(t *testing.T) (*task.TaskRunService, string, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "delete it").TaskRunID
	holdID := Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventID":"event-1"}`)}, nil)
	return taskRunService, taskRunID, holdID
}

var eventDeleteInput = json.RawMessage(`{ "eventID": "event-1" }`)

func TestAnOpenHoldIsPendingAndAnswersNoCall(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	holds := Holds(ledger.ListTaskEvent(taskRunID))

	if len(holds) != 1 || holds[0].ID != holdID || holds[0].State != StatePending {
		t.Fatalf("expected one pending hold, got %+v", holds)
	}
	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("a hold nobody approved must not answer the call")
	}
}

func TestAnApprovedHoldIsSpentByTheExactCallOnce(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	spent, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput)
	if !isSpent || spent.ID != holdID {
		t.Fatalf("expected the approved hold to answer the call, got %+v %v", spent, isSpent)
	}
	if _, isSpentTwice := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); isSpentTwice {
		t.Fatal("a spent approval must not answer a second call")
	}
}

func TestADifferentCallIsNotCoveredAndLeavesTheApprovalUnspent(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	for _, call := range []struct{ toolName, toolInput string }{{"event_delete", `{"eventID":"event-2"}`}, {"event_update", `{"eventID":"event-1"}`}} {
		if _, isSpent := SpendApprovedCall(ledger, taskRunID, call.toolName, json.RawMessage(call.toolInput)); isSpent {
			t.Fatalf("%+v must not spend the approval", call)
		}
	}
	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); !isSpent {
		t.Fatal("a refused different call must leave the approval spendable")
	}
}

func TestARejectedHoldAnswersNoCall(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionReject, "chat_reply")

	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("a rejection is not an approval")
	}
}

func TestAnApprovalInAnotherTaskRunIsNotCovered(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")
	otherTaskRunID := ledger.CreateTaskRun("person-1", "conversation-2", "other").TaskRunID

	if _, isSpent := SpendApprovedCall(ledger, otherTaskRunID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("an approval belongs to the task run it was given in")
	}
}

func replayed(source *task.TaskRunService, taskRunID string) (*task.TaskRunService, string) {
	reopened := task.NewTaskRunService(task.NewTaskEventService())
	reopenedID := reopened.CreateTaskRun("person-1", "conversation-1", "delete it").TaskRunID
	for _, taskEvent := range source.ListTaskEvent(taskRunID) {
		reopened.AppendTaskEvent(reopenedID, taskEvent.Name, taskEvent.Body)
	}
	return reopened, reopenedID
}

func TestAnApprovalAndItsSpendingSurviveARestart(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	reopened, reopenedID := replayed(ledger, taskRunID)
	if _, isSpent := SpendApprovedCall(reopened, reopenedID, "event_delete", eventDeleteInput); !isSpent {
		t.Fatal("an approval read back from stored events must be spendable")
	}
	reopenedAgain, againID := replayed(reopened, reopenedID)
	if _, isSpent := SpendApprovedCall(reopenedAgain, againID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("a spend read back from stored events must stay spent")
	}
}

func TestTheRecordWritesOnlyTheEventNamesTheLedgerAlreadyHad(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")
	SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput)

	names := []string{}
	for _, taskEvent := range ledger.ListTaskEvent(taskRunID)[1:] {
		names = append(names, taskEvent.Name)
	}
	expected := []string{agentcontract.TaskEventApprovalHoldOpened, agentcontract.TaskEventApprovalDecided, agentcontract.TaskEventApprovalHoldSpent}
	if len(names) < 3 {
		t.Fatalf("got %v", names)
	}
	for index, name := range expected {
		if names[len(names)-3+index] != name {
			t.Fatalf("expected %v, got %v", expected, names)
		}
	}
}

func TestAHoldWithAKnownToolNameAnswersOnlyThatTool(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_update", eventDeleteInput); isSpent {
		t.Fatal("a hold that names its tool answers no other tool")
	}
}

func namelessHold(t *testing.T, input string) (*task.TaskRunService, string) {
	t.Helper()
	ledger := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := ledger.CreateTaskRun("person-1", "conversation-1", "run it").TaskRunID
	holdID := Open(ledger, taskRunID, agentcontract.HeldCall{ToolInput: json.RawMessage(input)}, nil)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "harness_permission")
	return ledger, taskRunID
}

func TestAHoldOpenedWithoutAToolNameAnswersTheCallWithTheSameInput(t *testing.T) {
	ledger, taskRunID := namelessHold(t, `{"eventID":"event-1"}`)

	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); !isSpent {
		t.Fatal("a hold known only by its input answers the call with that input")
	}
}

func TestAHoldOpenedWithoutAToolNameAndWithoutInputAnswersNothing(t *testing.T) {
	for _, input := range []string{``, `{}`, `null`} {
		ledger, taskRunID := namelessHold(t, input)

		if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", json.RawMessage(input)); isSpent {
			t.Fatalf("input %q would let one approval cover every call that takes no arguments", input)
		}
	}
}

func scopedHold(t *testing.T) (*task.TaskRunService, string, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "send it").TaskRunID
	holdID := Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "message_send", ApprovalScope: "message_send:team"}, nil)
	return taskRunService, taskRunID, holdID
}

func scopeGrantCount(taskRunService *task.TaskRunService, taskRunID string) int {
	count := 0
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventApprovalScopeGranted {
			count++
		}
	}
	return count
}

func TestConfirmingAHoldGrantsItsScopeOnce(t *testing.T) {
	taskRunService, taskRunID, holdID := scopedHold(t)

	Decide(taskRunService, taskRunID, holdID, DecisionApprove, "chat_reply")
	Decide(taskRunService, taskRunID, holdID, DecisionApprove, "chat_reply")

	if grants := scopeGrantCount(taskRunService, taskRunID); grants != 1 {
		t.Fatalf("expected one scope grant, got %d", grants)
	}
}

func TestRejectingOrDeferringAHoldGrantsNoScope(t *testing.T) {
	for _, decision := range []string{DecisionReject, DecisionDefer} {
		taskRunService, taskRunID, _ := scopedHold(t)

		SettleLatest(taskRunService, taskRunID, decision, "chat_reply")

		if grants := scopeGrantCount(taskRunService, taskRunID); grants != 0 {
			t.Fatalf("%s must not grant scope, got %d grants", decision, grants)
		}
	}
}

func TestSettlingTheLatestHoldLeavesAnAlreadyDecidedHoldAlone(t *testing.T) {
	taskRunService, taskRunID, holdID := scopedHold(t)
	Decide(taskRunService, taskRunID, holdID, DecisionApprove, "chat_reply")
	eventCount := len(taskRunService.ListTaskEvent(taskRunID))

	SettleLatest(taskRunService, taskRunID, DecisionReject, "chat_reply")

	if len(taskRunService.ListTaskEvent(taskRunID)) != eventCount {
		t.Fatal("a decided hold must not be decided again")
	}
}

func TestTheChoicesOfferedWithAHoldTravelWithIt(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "update").TaskRunID
	offered := []Choice{{Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}, {Key: "now"}}
	Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "host_update"}, offered)

	recorded := OfferedChoices(taskRunService.ListTaskEvent(taskRunID))

	if len(recorded) != 2 || recorded[0] != offered[0] || recorded[1] != offered[1] {
		t.Fatalf("expected the offered choices in their order, got %+v", recorded)
	}
}

func TestALaterHoldDoesNotInheritTheChoicesOfAnEarlierOne(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "update").TaskRunID
	Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "host_update"}, []Choice{{Key: "now"}})
	Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete"}, nil)

	if recorded := OfferedChoices(taskRunService.ListTaskEvent(taskRunID)); len(recorded) != 0 {
		t.Fatalf("expected no choices for a hold that offered none, got %+v", recorded)
	}
}
