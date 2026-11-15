//go:build !nobundledharness

package approvalgate

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func TestARequesterWhoCannotBeAskedRefusesTheCallLoudlyAndNeverParksTheRun(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	poster.failure = errors.New("the messenger refuses this requester")

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionUnanswerable {
		t.Fatalf("a requester nobody can reach must see the call refused, got %q %v", outcome.Decision, errorValue)
	}
	if !carriesEvent(recordedEventNames(taskRunService, taskRun.TaskRunID), TaskEventApprovalUnreachable) {
		t.Fatal("the refusal must leave an event naming why")
	}
	if status := taskRunStatus(t, taskRunService, taskRun.TaskRunID); status == agentcontract.TaskStatusWaitingApproval {
		t.Fatalf("a call that can never be answered must not park the run, got %q", status)
	}
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
		t.Fatalf("the hold that could not be put to anyone must not stay pending, got %+v", holds)
	}
}

func TestAGateWithNoPosterRefusesTheCallInsteadOfHoldingIt(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseQuestionPoster(nil)

	outcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if outcome.Decision != mcpserver.ApprovalDecisionUnanswerable {
		t.Fatalf("with nobody to ask the call is refused, got %q", outcome.Decision)
	}
	if !carriesEvent(recordedEventNames(taskRunService, taskRun.TaskRunID), TaskEventApprovalUnreachable) {
		t.Fatal("the refusal must leave an event naming why")
	}
	if len(holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))) != 0 {
		t.Fatal("no hold is opened for a question nobody can be asked")
	}
}
