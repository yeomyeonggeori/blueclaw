//go:build !nobundledharness

package approvalgate

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

type endingAsker struct{ status AskStatus }

func (asker endingAsker) AskPermission(context.Context, mcpserver.ApprovalRequest, PermissionQuestion) (ApprovalAnswer, AskStatus) {
	return ApprovalAnswer{}, asker.status
}

func TestARequesterWhoCannotBeAskedRefusesTheCallLoudlyAndNeverParksTheRun(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(endingAsker{status: AskUnreachable})

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

func TestAGateWithNoAskerRefusesTheCallInsteadOfHoldingIt(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(nil)

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

func TestAnExpiredQuestionRejectsTheHoldAndTellsTheRunWhy(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(endingAsker{status: AskExpired})

	outcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if outcome.Decision != mcpserver.ApprovalDecisionRejected || !strings.Contains(outcome.Notice, "24 hours") {
		t.Fatalf("an unanswered question is rejected and says why, got %q %q", outcome.Decision, outcome.Notice)
	}
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
		t.Fatalf("an expired hold is rejected, got %+v", holds)
	}
	if !carriesEvent(recordedEventNames(taskRunService, taskRun.TaskRunID), TaskEventApprovalExpired) {
		t.Fatal("the expiry must leave an event")
	}
	if status := taskRunStatus(t, taskRunService, taskRun.TaskRunID); status == agentcontract.TaskStatusWaitingApproval {
		t.Fatalf("the run is told and continues, got %q", status)
	}
}

func TestAnInterruptedQuestionLeavesTheHoldPendingForTheRestartToAwait(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)

	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].State != holdrecord.StatePending {
		t.Fatalf("an interrupted ask keeps its hold pending, got %+v", holds)
	}
}
