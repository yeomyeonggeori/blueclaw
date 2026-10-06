package approvalgate

import (
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func scopedHold(t *testing.T) (*task.TaskRunService, string, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "send it").TaskRunID
	hold := holdrecord.Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "message_send", ApprovalScope: "message_send:team"}, nil)
	return taskRunService, taskRunID, hold.ID
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

func TestRejectingOrDeferringTheLatestHoldGrantsNoScope(t *testing.T) {
	for _, decision := range []string{holdrecord.DecisionReject, holdrecord.DecisionDefer} {
		taskRunService, taskRunID, _ := scopedHold(t)

		SettleLatest(taskRunService, taskRunID, decision, "chat_reply")

		if grants := scopeGrantCount(taskRunService, taskRunID); grants != 0 {
			t.Fatalf("%s must not grant scope, got %d grants", decision, grants)
		}
	}
}

func TestSettlingTheLatestHoldLeavesAnAlreadyDecidedHoldAlone(t *testing.T) {
	taskRunService, taskRunID, holdID := scopedHold(t)
	holdrecord.Decide(taskRunService, taskRunID, holdID, holdrecord.DecisionApprove, "chat_reply")
	eventCount := len(taskRunService.ListTaskEvent(taskRunID))

	SettleLatest(taskRunService, taskRunID, holdrecord.DecisionReject, "chat_reply")

	if len(taskRunService.ListTaskEvent(taskRunID)) != eventCount {
		t.Fatal("a decided hold must not be decided again")
	}
}

func TestASignalSettlesTheLatestHoldAndAnUnknownOneSettlesNothing(t *testing.T) {
	taskRunService, taskRunID, _ := scopedHold(t)
	unknown := agentcontract.ApprovalSignal("maybe")
	approve := agentcontract.ApprovalSignalApprove
	eventCount := len(taskRunService.ListTaskEvent(taskRunID))

	SettleSignal(taskRunService, taskRunID, &unknown, "chat_reply")
	SettleSignal(taskRunService, taskRunID, nil, "chat_reply")
	if len(taskRunService.ListTaskEvent(taskRunID)) != eventCount {
		t.Fatal("a signal that is not approve or reject decides nothing")
	}

	SettleSignal(taskRunService, taskRunID, &approve, "chat_reply")
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRunID)); holds[0].State != holdrecord.StateApproved {
		t.Fatalf("approve settles the latest pending hold, got %+v", holds)
	}
}

func TestTheChoicesOfferedWithAHoldAreTheLatestHoldsChoices(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "update").TaskRunID
	offered := []holdrecord.Choice{{Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}, {Key: "now"}}
	holdrecord.Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "host_update"}, offered)

	recorded := OfferedChoices(taskRunService.ListTaskEvent(taskRunID))

	if len(recorded) != 2 || recorded[0] != offered[0] || recorded[1] != offered[1] {
		t.Fatalf("expected the offered choices in their order, got %+v", recorded)
	}
}

func TestALaterHoldDoesNotInheritTheChoicesOfAnEarlierOne(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "update").TaskRunID
	holdrecord.Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "host_update"}, []holdrecord.Choice{{Key: "now"}})
	holdrecord.Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete"}, nil)

	if recorded := OfferedChoices(taskRunService.ListTaskEvent(taskRunID)); len(recorded) != 0 {
		t.Fatalf("expected no choices for a hold that offered none, got %+v", recorded)
	}
}
