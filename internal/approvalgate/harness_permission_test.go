//go:build !nobundledharness

package approvalgate

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

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

func TestAHarnessQuestionParksTheRunLikeAnyHeldCall(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)

	_, isAnswered := gate.AskHarnessPermission(context.Background(), approvalRequestFixture(taskRun.TaskRunID), harnessQuestion(map[string]any{"branch": "main"}))

	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if isAnswered || len(holds) != 1 || holds[0].State != holdrecord.StatePending || holds[0].Call.Confirmation != "Force-push the branch?" || holds[0].Call.ToolName != "" {
		t.Fatalf("a harness question nobody has answered is a pending hold, got %+v answered=%v", holds, isAnswered)
	}
	if len(poster.questions) != 1 || poster.questions[0].HoldID != holds[0].ID || poster.questions[0].Confirmation != "Force-push the branch?" {
		t.Fatalf("the harness's question is posted once as that hold's question, got %+v", poster.questions)
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) != agentcontract.TaskStatusWaitingApproval {
		t.Fatal("the run must be parked until the answer arrives as a message")
	}
	if !carriesEvent(recordedEventNames(taskRunService, taskRun.TaskRunID), agentcontract.TaskEventAskRequested) {
		t.Fatal("the harness turn ends on the ask the ledger records, and none was recorded")
	}
}

func TestAnApprovedHarnessQuestionAllowsTheSameCallOnceWhenTheHarnessAsksAgain(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	request := approvalRequestFixture(taskRun.TaskRunID)
	input := map[string]any{"branch": "main"}
	gate.AskHarnessPermission(context.Background(), request, harnessQuestion(input))
	if _, errorValue := gate.SettleAnswer(context.Background(), request, ApproveOptionKey, "test"); errorValue != nil {
		t.Fatalf("settle answer: %v", errorValue)
	}

	outcome, isAnswered := gate.AskHarnessPermission(context.Background(), request, harnessQuestion(input))

	if !isAnswered || outcome.Selected == nil || outcome.Selected.OptionId != "allow-once" || len(poster.questions) != 1 {
		t.Fatalf("the approved call must be allowed without a second question, got %+v asked %d", outcome, len(poster.questions))
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) == agentcontract.TaskStatusWaitingApproval {
		t.Fatal("an answered run must not stay parked")
	}
	gate.AskHarnessPermission(context.Background(), request, harnessQuestion(input))
	if len(poster.questions) != 2 {
		t.Fatalf("the approval is spent once, so the third ask reaches the person, asked %d", len(poster.questions))
	}
}

func TestARejectedHarnessQuestionIsARejectedHold(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	request := approvalRequestFixture(taskRun.TaskRunID)
	gate.AskHarnessPermission(context.Background(), request, harnessQuestion(nil))

	if _, errorValue := gate.SettleAnswer(context.Background(), request, RejectOptionKey, "test"); errorValue != nil {
		t.Fatalf("settle answer: %v", errorValue)
	}

	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
		t.Fatalf("expected a rejected hold, got %+v", holds)
	}
	if _, isAnswered := gate.AskHarnessPermission(context.Background(), request, harnessQuestion(nil)); isAnswered || len(poster.questions) != 2 {
		t.Fatalf("a declined call is not allowed when the harness asks again, answered=%v asked %d", isAnswered, len(poster.questions))
	}
}

func TestADifferentHarnessCallIsNotCoveredByAnApproval(t *testing.T) {
	gate, _, taskRun, poster := gateWithPoster(t)
	request := approvalRequestFixture(taskRun.TaskRunID)
	gate.AskHarnessPermission(context.Background(), request, harnessQuestion(map[string]any{"branch": "main"}))
	gate.SettleAnswer(context.Background(), request, ApproveOptionKey, "test")

	_, isAnswered := gate.AskHarnessPermission(context.Background(), request, harnessQuestion(map[string]any{"branch": "release"}))

	if isAnswered || len(poster.questions) != 2 {
		t.Fatalf("a call that differs in any way is asked again, answered=%v asked %d", isAnswered, len(poster.questions))
	}
}

func approvedHarnessQuestionAboutTheDeleteInput(t *testing.T) (*Gate, *recordingPoster, string) {
	t.Helper()
	gate, _, taskRun, poster := gateWithPoster(t)
	request := approvalRequestFixture(taskRun.TaskRunID)
	gate.AskHarnessPermission(context.Background(), request, harnessQuestion(map[string]any{"eventID": "event-1"}))
	if _, errorValue := gate.SettleAnswer(context.Background(), request, ApproveOptionKey, "test"); errorValue != nil {
		t.Fatalf("settle answer: %v", errorValue)
	}
	return gate, poster, taskRun.TaskRunID
}

func TestAHarnessThatAskedFirstLeavesTheMatchingToolCallRunningWithNoSecondQuestion(t *testing.T) {
	gate, poster, taskRunID := approvedHarnessQuestionAboutTheDeleteInput(t)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRunID))

	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("expected the call to run on the harness's approved hold, got %+v %v", outcome, errorValue)
	}
	if len(poster.questions) != 1 {
		t.Fatalf("the person was asked %d questions, expected only the harness's", len(poster.questions))
	}
}

func TestAnApprovedHoldIsSpentByOneToolCall(t *testing.T) {
	gate, poster, taskRunID := approvedHarnessQuestionAboutTheDeleteInput(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRunID))

	outcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRunID))

	if outcome.Decision == mcpserver.ApprovalDecisionApproved || len(poster.questions) != 2 {
		t.Fatalf("a second identical call needs its own approval, got %+v after %d questions", outcome, len(poster.questions))
	}
}

func TestAToolCallWithADifferentInputIsAskedAbout(t *testing.T) {
	gate, poster, taskRunID := approvedHarnessQuestionAboutTheDeleteInput(t)
	request := approvalRequestFixture(taskRunID)
	request.ToolInput = []byte(`{"eventID":"event-2"}`)

	outcome, _ := gate.AwaitApproval(context.Background(), request)

	if outcome.Decision == mcpserver.ApprovalDecisionApproved || len(poster.questions) != 2 {
		t.Fatalf("a call that differs in any way is asked again, got %+v after %d questions", outcome, len(poster.questions))
	}
}

func TestAGatedCallNoHarnessAskedAboutIsAskedOnce(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)

	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(poster.questions) != 1 || len(holds) != 1 || holds[0].Call.ToolName != "event_delete" {
		t.Fatalf("expected one question held under the tool's name, got %d questions and %+v", len(poster.questions), holds)
	}
}

func TestAHarnessQuestionIsWordedLikeEveryApprovalInTheRequestersVoice(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	languageModel := &wordingLanguageModel{question: "브랜치를 강제로 올릴까요?"}
	gate.UseQuestionWorder(approval.NewWorder(languageModel))
	request := approvalRequestFixture(taskRun.TaskRunID)
	request.ToolName = "Force-push the branch to origin"
	request.ToolInput = []byte(`{"command":"git push --force"}`)
	request.Voice = "- Call them 박예시 님."

	gate.AskHarnessPermission(context.Background(), request, harnessQuestion(map[string]any{"command": "git push --force"}))

	if len(poster.questions) != 1 || poster.questions[0].Confirmation != "브랜치를 강제로 올릴까요?" {
		t.Fatalf("the person reads the worded question, not the harness's own title, got %+v", poster.questions)
	}
	if wordingContext := marshalRequestMessages(languageModel.lastRequest); !strings.Contains(wordingContext, "Call them 박예시 님.") || !strings.Contains(wordingContext, "git push --force") {
		t.Fatalf("the wording is told the call and the voice, got %s", wordingContext)
	}
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].Call.Confirmation != "브랜치를 강제로 올릴까요?" {
		t.Fatalf("the ledger keeps what the person was asked, got %+v", holds)
	}
}
