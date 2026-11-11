//go:build !nobundledharness

package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func roomQuestion(taskRunID string) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		TaskRunID:         taskRunID,
		ToolName:          "ask_input",
		ToolInput:         json.RawMessage(`{"question":"어느 방으로 할까요?","choices":["회의실 A","회의실 B"]}`),
	}
}

func TestAQuestionWithChoicesIsHeldWithItsOptionsAsLabelledChoices(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)

	outcome, errorValue := gate.AwaitApproval(context.Background(), roomQuestion(taskRun.TaskRunID))

	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a question nobody answers yet is held, got %+v %v", outcome, errorValue)
	}
	if outcome.Notice != "어느 방으로 할까요?" {
		t.Fatalf("the question is held as asked, without its options numbered into it, got %q", outcome.Notice)
	}
	offered := OfferedChoices(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(offered) != 2 || offered[0].Label != "회의실 A" || offered[1].Key != "2" {
		t.Fatalf("the ledger keeps the options so a reply read after a restart is read against them, got %+v", offered)
	}
}

func TestAQuestionAskedInTheThreadIsAnsweredByThePickedOption(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UsePermissionAsker(&choosingAsker{answer: ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: "2"}})

	outcome, errorValue := gate.AwaitApproval(context.Background(), roomQuestion(taskRun.TaskRunID))

	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("a picked option answers the question, got %+v %v", outcome, errorValue)
	}
	chosen, isChosen := AnswerChosen(taskRunService.ListTaskEvent(taskRun.TaskRunID), AskedChoices("ask_input", roomQuestion("").ToolInput))
	if !isChosen || chosen.Text != "회의실 B" || chosen.ChoiceKey != "2" {
		t.Fatalf("the pick is recorded for the tool that returns it, got %+v %v", chosen, isChosen)
	}
}

func TestAnAnswerInTheRequestersOwnWordsIsWhatTheQuestionReturns(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	words := "회의실 C로 잡아줘, 열 명이야"
	gate.UsePermissionAsker(&ownWordsAsker{taskRunService: taskRunService, words: words})

	outcome, errorValue := gate.AwaitApproval(context.Background(), roomQuestion(taskRun.TaskRunID))

	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("an answer in the requester's own words answers the question, got %+v %v", outcome, errorValue)
	}
	result := ChosenAnswerResult(taskRunService.ListTaskEvent(taskRun.TaskRunID), roomQuestion("").ToolInput)
	if result.Failure != nil || result.Output.Content != "The requester answered in their own words: "+words || strings.Contains(string(result.Output.Data), "choiceKey") {
		t.Fatalf("the question returns the requester's own words and no picked option, got %+v", result)
	}
}

type ownWordsAsker struct {
	taskRunService *task.TaskRunService
	words          string
}

func (asker *ownWordsAsker) AskPermission(_ context.Context, approvalRequest mcpserver.ApprovalRequest, _ PermissionQuestion) (ApprovalAnswer, AskStatus) {
	RecordOwnWordsAnswer(asker.taskRunService, approvalRequest.TaskRunID, asker.words)
	return ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove}, AskAnswered
}

func TestOnlyAQuestionWhoseOptionsAreAnswersTakesOwnWords(t *testing.T) {
	if !TakesOwnWords(AskedChoices("ask_input", roomQuestion("").ToolInput)) {
		t.Fatal("a question whose options are answers takes an answer in the requester's own words")
	}
	if TakesOwnWords([]holdrecord.Choice{{Key: "now"}, {Key: "later", StartsAt: "2099-10-03T03:00:00+09:00"}}) {
		t.Fatal("approving a call now or later is not a question with an answer of its own")
	}
}

func TestAQuestionWithChoicesNeedsTheGateButAFreeQuestionDoesNot(t *testing.T) {
	definition := toolcontract.ToolDefinition{Name: "ask_input"}

	if !callNeedsApproval(definition, json.RawMessage(`{"question":"q","choices":["a"]}`)) {
		t.Fatal("a question with fixed options is a hold")
	}
	if callNeedsApproval(definition, json.RawMessage(`{"question":"q"}`)) || callNeedsApproval(definition, json.RawMessage(`{"question":"q","choices":[]}`)) {
		t.Fatal("a question with no fixed options is answered by the next message")
	}
}
