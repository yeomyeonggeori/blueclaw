//go:build !nobundledharness

package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func spentApprovalEventBodies(taskRunService *task.TaskRunService, taskRunID string) []string {
	bodies := []string{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventApprovalHoldSpent {
			bodies = append(bodies, taskEvent.Body)
		}
	}
	return bodies
}

func TestTheSpentApprovalCarriesTheCallAndTheHoldItSpent(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	holdID := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))[0].ID
	recordDecision(taskRunService, taskRun.TaskRunID, "approve")

	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	bodies := spentApprovalEventBodies(taskRunService, taskRun.TaskRunID)
	if len(bodies) != 1 {
		t.Fatalf("one approval is spent once, and by one writer: %v", bodies)
	}
	for _, expectedFragment := range []string{`"toolName":"event_delete"`, `"eventID":"event-1"`, `"holdID":"` + holdID + `"`} {
		if !strings.Contains(bodies[0], expectedFragment) {
			t.Fatalf("expected the spent approval to carry %q, got %s", expectedFragment, bodies[0])
		}
	}
	if strings.Contains(bodies[0], "approvalToken") {
		t.Fatalf("a hold has one id, got %s", bodies[0])
	}
}

func TestAHoldIsSpentOnceSoASecondCallDoesNotClaimIt(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	holdID := holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventID":"event-1"}`)}, nil).ID
	holdrecord.Decide(taskRunService, taskRun.TaskRunID, holdID, holdrecord.DecisionApprove, "chat_reply")

	firstHoldID := RecordApprovalSpent(taskRunService, taskRun.TaskRunID, "event_delete", json.RawMessage(`{"eventID":"event-1"}`))
	secondHoldID := RecordApprovalSpent(taskRunService, taskRun.TaskRunID, "event_delete", json.RawMessage(`{"eventID":"event-2"}`))

	if firstHoldID != holdID || secondHoldID != "" {
		t.Fatalf("the first call spends the approved hold and the next claims none, got %q then %q", firstHoldID, secondHoldID)
	}
	if bodies := spentApprovalEventBodies(taskRunService, taskRun.TaskRunID); len(bodies) != 2 {
		t.Fatalf("expected both calls to be recorded, got %v", bodies)
	}
}
