package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
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
	if !strings.Contains(outcome.Notice, "어느 방으로 할까요?") || !strings.Contains(outcome.Notice, "2. 회의실 B") {
		t.Fatalf("the person reads the question and every option, got %q", outcome.Notice)
	}
	offered := approvalrecord.OfferedChoices(taskRunService.ListTaskEvent(taskRun.TaskRunID))
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
	chosen, isChosen := approvalrecord.AnswerChosen(taskRunService.ListTaskEvent(taskRun.TaskRunID), approvalrecord.AskedChoices("ask_input", roomQuestion("").ToolInput))
	if !isChosen || chosen.Label != "회의실 B" {
		t.Fatalf("the pick is recorded for the tool that returns it, got %+v %v", chosen, isChosen)
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
