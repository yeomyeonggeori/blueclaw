package approvalgate

import (
	"context"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type harnessAskerDouble struct {
	questions         []HarnessPermissionQuestion
	gateQuestionCount int
	outcome           acp.RequestPermissionOutcome
	isAnswered        bool
}

func (asker *harnessAskerDouble) AskPermission(context.Context, mcpserver.ApprovalRequest, PermissionQuestion) (ApprovalAnswer, AskStatus) {
	asker.gateQuestionCount++
	return ApprovalAnswer{}, AskInterrupted
}

func (asker *harnessAskerDouble) AskHarnessPermission(_ context.Context, _ mcpserver.ApprovalRequest, question HarnessPermissionQuestion) (acp.RequestPermissionOutcome, AskStatus) {
	asker.questions = append(asker.questions, question)
	return asker.outcome, statusOfAnswered(asker.isAnswered)
}

func harnessQuestion(rawInput map[string]any) HarnessPermissionQuestion {
	title := "git_push"
	return HarnessPermissionQuestion{
		Text:     "Force-push the branch?",
		ToolCall: acp.ToolCallUpdate{ToolCallId: "harness-call-1", Title: &title, RawInput: rawInput},
		Options: []acp.PermissionOption{
			{OptionId: "allow-once", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "reject-once", Kind: acp.PermissionOptionKindRejectOnce},
		},
	}
}

func chosen(optionID acp.PermissionOptionId) acp.RequestPermissionOutcome {
	return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{Outcome: "selected", OptionId: optionID}}
}

func TestAHarnessQuestionIsRecordedAsAHoldBeforeAnyoneIsAsked(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	asker := &harnessAskerDouble{}
	gate.UsePermissionAsker(asker)

	_, status := gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(map[string]any{"branch": "main"}))

	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if (status == AskAnswered) || len(holds) != 1 || holds[0].State != holdrecord.StatePending || holds[0].Call.Confirmation != "Force-push the branch?" || holds[0].Call.ToolName != "" {
		t.Fatalf("an unanswered harness question must stay a pending hold a restart can reissue, got %+v answered=%v", holds, (status == AskAnswered))
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) != agentcontract.TaskStatusWaitingApproval {
		t.Fatal("the run must wait for the answer the reissue will collect")
	}
}

func TestAnAnsweredHarnessQuestionSettlesItsHoldAndTheRunContinues(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(&harnessAskerDouble{outcome: chosen("allow-once"), isAnswered: true})

	outcome, status := gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(map[string]any{"branch": "main"}))

	if status != AskAnswered || outcome.Selected == nil || outcome.Selected.OptionId != "allow-once" {
		t.Fatalf("expected the answer to pass through, got %+v", outcome)
	}
	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(holds) != 1 || holds[0].State != holdrecord.StateApproved {
		t.Fatalf("an approval stays unspent until exactly one call spends it, got %+v", holds)
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) == agentcontract.TaskStatusWaitingApproval {
		t.Fatal("an answered run must not stay paused")
	}
}

func TestARejectedHarnessQuestionIsARejectedHold(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(&harnessAskerDouble{outcome: chosen("reject-once"), isAnswered: true})

	gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(nil))

	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
		t.Fatalf("expected a rejected hold, got %+v", holds)
	}
}

func TestARetryOfAnApprovedHarnessCallIsNotAskedAgainAndSpendsTheApprovalOnce(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	asker := &harnessAskerDouble{}
	gate.UsePermissionAsker(asker)
	input := map[string]any{"branch": "main"}
	gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(input))
	recordDecision(taskRunService, taskRun.TaskRunID, "approve")
	asker.questions = nil

	outcome, status := gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(input))

	if status != AskAnswered || outcome.Selected == nil || outcome.Selected.OptionId != "allow-once" || len(asker.questions) != 0 {
		t.Fatalf("the approved call must run without a second question, got %+v asked %d", outcome, len(asker.questions))
	}
	gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(input))
	if len(asker.questions) != 1 {
		t.Fatalf("the approval is spent once, so the third ask reaches the person, asked %d", len(asker.questions))
	}
}

func TestADifferentHarnessCallIsNotCoveredByAnApproval(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	asker := &harnessAskerDouble{}
	gate.UsePermissionAsker(asker)
	gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(map[string]any{"branch": "main"}))
	recordDecision(taskRunService, taskRun.TaskRunID, "approve")
	asker.questions = nil

	gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(map[string]any{"branch": "release"}))

	if len(asker.questions) != 1 {
		t.Fatalf("a call that differs in any way is asked again, asked %d", len(asker.questions))
	}
}

func approvedHarnessQuestionAboutTheDeleteInput(t *testing.T) (*Gate, *harnessAskerDouble, string) {
	t.Helper()
	gate, _, taskRun := gateFixture(t)
	asker := &harnessAskerDouble{outcome: chosen("allow-once"), isAnswered: true}
	gate.UsePermissionAsker(asker)
	gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(map[string]any{"eventID": "event-1"}))
	return gate, asker, taskRun.TaskRunID
}

func TestAHarnessThatAskedFirstLeavesTheMatchingToolCallRunningWithNoSecondQuestion(t *testing.T) {
	gate, asker, taskRunID := approvedHarnessQuestionAboutTheDeleteInput(t)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRunID))

	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected the call to run on the harness's approved hold, got %+v %v", outcome, errorValue)
	}
	if asker.gateQuestionCount != 0 || len(asker.questions) != 1 {
		t.Fatalf("the person was asked %d gate questions and %d harness questions, expected only the harness's", asker.gateQuestionCount, len(asker.questions))
	}
}

func TestAnApprovedHoldIsSpentByOneToolCall(t *testing.T) {
	gate, asker, taskRunID := approvedHarnessQuestionAboutTheDeleteInput(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRunID))

	outcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRunID))

	if outcome.Decision == mcpserver.ApprovalDecisionApproved || asker.gateQuestionCount != 1 {
		t.Fatalf("a second identical call needs its own approval, got %+v after %d questions", outcome, asker.gateQuestionCount)
	}
}

func TestAToolCallWithADifferentInputIsAskedAbout(t *testing.T) {
	gate, asker, taskRunID := approvedHarnessQuestionAboutTheDeleteInput(t)
	request := approvalRequestFixture(taskRunID)
	request.ToolInput = []byte(`{"eventID":"event-2"}`)

	outcome, _ := gate.AwaitApproval(context.Background(), request)

	if outcome.Decision == mcpserver.ApprovalDecisionApproved || asker.gateQuestionCount != 1 {
		t.Fatalf("a call that differs in any way is asked again, got %+v after %d questions", outcome, asker.gateQuestionCount)
	}
}

func TestAGatedCallNoHarnessAskedAboutIsAskedOnce(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	asker := &harnessAskerDouble{}
	gate.UsePermissionAsker(asker)

	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if asker.gateQuestionCount != 1 || len(holds) != 1 || holds[0].Call.ToolName != "event_delete" {
		t.Fatalf("expected one question held under the tool's name, got %d questions and %+v", asker.gateQuestionCount, holds)
	}
}
