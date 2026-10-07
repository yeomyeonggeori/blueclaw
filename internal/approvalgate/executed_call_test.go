//go:build !nobundledharness

package approvalgate

import (
	"context"
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
