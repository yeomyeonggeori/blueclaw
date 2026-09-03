package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func spentApprovalEventBodies(taskRunService *task.TaskRunService, taskRunID string) []string {
	bodies := []string{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventApprovalExecuted {
			bodies = append(bodies, taskEvent.Body)
		}
	}
	return bodies
}

func TestTheSpentApprovalCarriesTheCallAndTheTokenTheLoopIsWaitingOn(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventApprovalHeldCall,
		`{"approvalToken":"token-1","toolName":"event_delete","toolInput":{"eventID":"event-1"},"observationID":"obs-1"}`)
	recordDecision(taskRunService, taskRun.TaskRunID, "confirm")

	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	bodies := spentApprovalEventBodies(taskRunService, taskRun.TaskRunID)
	if len(bodies) != 1 {
		t.Fatalf("one approval is spent once, and by one writer: %v", bodies)
	}
	for _, expectedFragment := range []string{`"toolName":"event_delete"`, `"eventID":"event-1"`, `"approvalToken":"token-1"`} {
		if !strings.Contains(bodies[0], expectedFragment) {
			t.Fatalf("expected the spent approval to carry %q, got %s", expectedFragment, bodies[0])
		}
	}
}

func TestATokenIsSpentOnceSoASecondApprovalDoesNotClaimIt(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventApprovalHeldCall,
		`{"approvalToken":"token-1","toolName":"event_delete","toolInput":{"eventID":"event-1"},"observationID":"obs-1"}`)

	RecordApprovalSpent(taskRunService, taskRun.TaskRunID, "event_delete", json.RawMessage(`{"eventID":"event-1"}`))
	RecordApprovalSpent(taskRunService, taskRun.TaskRunID, "event_delete", json.RawMessage(`{"eventID":"event-2"}`))

	bodies := spentApprovalEventBodies(taskRunService, taskRun.TaskRunID)
	if len(bodies) != 2 {
		t.Fatalf("expected both calls to be recorded, got %v", bodies)
	}
	if !strings.Contains(bodies[0], `"approvalToken":"token-1"`) {
		t.Fatalf("expected the first call to spend the hold that was waiting, got %s", bodies[0])
	}
	if strings.Contains(bodies[1], "approvalToken") {
		t.Fatalf("a hold that is already spent is not spent again by the next call, got %s", bodies[1])
	}
}
