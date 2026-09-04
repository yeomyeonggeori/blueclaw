package approvalgate

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type scriptedAsker struct {
	answer     agentcontract.ApprovalSignal
	isAnswered bool
	askedCount int
}

func (asker *scriptedAsker) AskPermission(context.Context, mcpserver.ApprovalRequest, string) (agentcontract.ApprovalSignal, bool) {
	asker.askedCount++
	return asker.answer, asker.isAnswered
}

func recordedEventNames(taskRunService *task.TaskRunService, taskRunID string) []string {
	names := []string{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		names = append(names, taskEvent.Name)
	}
	return names
}

func carriesEvent(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}

func taskRunStatus(t *testing.T, taskRunService *task.TaskRunService, taskRunID string) agentcontract.TaskStatus {
	t.Helper()
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		t.Fatalf("no task run %s", taskRunID)
	}
	return taskRun.Status
}

func TestAnAnsweredCallRunsInsideTheTurnAndReadsTheSameOnTheLedger(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	asker := &scriptedAsker{answer: agentcontract.ApprovalSignalApprove, isAnswered: true}
	gate.UsePermissionAsker(asker)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("the answered call decided %q, expected approved", outcome.Decision)
	}
	if asker.askedCount != 1 {
		t.Fatalf("the person was asked %d times, expected once", asker.askedCount)
	}
	names := recordedEventNames(taskRunService, taskRun.TaskRunID)
	for _, wanted := range []string{
		agentcontract.TaskEventApprovalPendingCall,
		agentcontract.TaskEventConfirmationRequested,
		agentcontract.TaskEventApprovalDecided,
		agentcontract.TaskEventApprovalExecuted,
	} {
		if !carriesEvent(names, wanted) {
			t.Fatalf("the ledger carries %v and not %s, so an acp-answered turn reads differently from a chat-answered one", names, wanted)
		}
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) == agentcontract.TaskStatusWaitingApproval {
		t.Fatal("the run was paused for an approval that had already been answered")
	}
}

func TestACallNobodyCanBeAskedAboutIsStillHeld(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(&scriptedAsker{isAnswered: false})

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("the unanswerable call decided %q, expected held", outcome.Decision)
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) != agentcontract.TaskStatusWaitingApproval {
		t.Fatal("a held call left the run running, so nobody can see it is waiting")
	}
}

func TestADeclinedCallIsRejectedAndNotRecordedAsExecuted(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(&scriptedAsker{answer: agentcontract.ApprovalSignalReject, isAnswered: true})

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionRejected {
		t.Fatalf("the declined call decided %q, expected rejected", outcome.Decision)
	}
	if carriesEvent(recordedEventNames(taskRunService, taskRun.TaskRunID), agentcontract.TaskEventApprovalExecuted) {
		t.Fatal("a declined call was recorded as executed")
	}
}

func TestAGateWithNoAskerHoldsTheCallItAlwaysHeld(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("the connectors path decided %q, expected held", outcome.Decision)
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) != agentcontract.TaskStatusWaitingApproval {
		t.Fatal("the connectors path stopped pausing the run it holds")
	}
}
