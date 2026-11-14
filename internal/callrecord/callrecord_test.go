package callrecord

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type appendedEvent struct {
	taskRunID string
	name      string
	body      string
}

type ledgerFake struct {
	events []appendedEvent
}

func (ledger *ledgerFake) AppendTaskEvent(taskRunID string, name string, body string) {
	ledger.events = append(ledger.events, appendedEvent{taskRunID: taskRunID, name: name, body: body})
}

var messageSend = toolcontract.ToolDescriptor{Name: "message_send", SideEffectClass: "external_send"}

func TestACallLeavesItsRequestAndItsResultUnderOneObservation(t *testing.T) {
	ledger := &ledgerFake{}
	input := json.RawMessage(`{"targetType":"currentThread","message":"살아있어요"}`)

	Start(ledger, "run-1", messageSend, input).Finish(toolcontract.ToolSuccess("보냈습니다"), nil)

	if len(ledger.events) != 2 || ledger.events[0].name != "tool.message_send.requested" || ledger.events[1].name != "tool.message_send.result" {
		t.Fatalf("expected a requested and a result event, got %+v", ledger.events)
	}
	requested := agentcontract.ToolCallRecordOf[agentcontract.ToolCallRequested](ledger.events[0].body)
	result := agentcontract.ToolCallRecordOf[agentcontract.ToolCallResult](ledger.events[1].body)
	if requested.ObservationID == "" || requested.ObservationID != result.ObservationID {
		t.Fatalf("expected the request and its result to share one observation, got %q and %q", requested.ObservationID, result.ObservationID)
	}
	if requested.SideEffectClass != toolcontract.ToolSideEffectExternalSend || string(requested.Input) != string(input) {
		t.Fatalf("expected the request to carry what the call does and what it was given, got %+v", requested)
	}
	if !result.Succeeded() || result.Summary != "보냈습니다" || string(result.ToolInput) != string(input) {
		t.Fatalf("expected the result to say the call succeeded with its answer and input, got %+v", result)
	}
}

func TestACallThatCouldNotRunIsRecordedAsAFailure(t *testing.T) {
	ledger := &ledgerFake{}

	Start(ledger, "run-1", messageSend, json.RawMessage(`{}`)).Finish(toolcontract.ToolResult{}, errors.New("chatd is not reachable"))

	result := agentcontract.ToolCallRecordOf[agentcontract.ToolCallResult](ledger.events[1].body)
	if result.Succeeded() || !strings.Contains(result.Summary, "chatd is not reachable") {
		t.Fatalf("expected a call that never ran to be recorded as a failure naming why, got %+v", result)
	}
}

func TestALongAnswerIsShortenedInTheRecord(t *testing.T) {
	ledger := &ledgerFake{}

	Start(ledger, "run-1", toolcontract.ToolDescriptor{Name: "file_read"}, nil).Finish(toolcontract.ToolSuccess(strings.Repeat("가", summaryCharacterLimit*3)), nil)

	if summary := agentcontract.ToolCallRecordOf[agentcontract.ToolCallResult](ledger.events[1].body).Summary; len([]rune(summary)) > summaryCharacterLimit+1 {
		t.Fatalf("expected the recorded answer to stop near %d characters, got %d", summaryCharacterLimit, len([]rune(summary)))
	}
}

func TestNothingIsRecordedWithoutALedgerOrARun(t *testing.T) {
	ledger := &ledgerFake{}

	Start(nil, "run-1", messageSend, nil).Finish(toolcontract.ToolSuccess("보냈습니다"), nil)
	Start(ledger, " ", messageSend, nil).Finish(toolcontract.ToolSuccess("보냈습니다"), nil)

	if len(ledger.events) != 0 {
		t.Fatalf("expected no record without a ledger or a run to record into, got %+v", ledger.events)
	}
}

func TestACallCarriedOutAfterApprovalIsRecordedWithWhatItDoes(t *testing.T) {
	ledger := &ledgerFake{}
	toolSet := toolcontract.NewToolSet([]string{"message_send"})
	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[struct{}, toolcontract.ToolResult]{
		Definition: messageSend,
		Handler: func(_ context.Context, _ struct{}) (toolcontract.ToolResult, error) {
			return toolcontract.ToolSuccess("unused"), nil
		},
	})

	RecordCarriedOut(ledger, "run-1", toolSet, []agentcontract.CarriedOutCall{{ToolName: "message_send", ToolInput: json.RawMessage(`{"targetType":"currentThread"}`), Result: toolcontract.ToolSuccess("보냈습니다")}})

	requested := agentcontract.ToolCallRecordOf[agentcontract.ToolCallRequested](ledger.events[0].body)
	result := agentcontract.ToolCallRecordOf[agentcontract.ToolCallResult](ledger.events[1].body)
	if requested.SideEffectClass != toolcontract.ToolSideEffectExternalSend || !result.Succeeded() || result.ObservationID != requested.ObservationID {
		t.Fatalf("expected the carried-out call to be recorded like any call the host ran, got %+v and %+v", requested, result)
	}
}
