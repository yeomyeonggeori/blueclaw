package acpharness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func runTurnCallingNoteWrite(t *testing.T, agentAuthorsItsLedger bool, carriedOutCalls ...agentcontract.CarriedOutCall) []agentcontract.TaskEvent {
	t.Helper()
	taskRuns, taskRunID := newTaskRunStore()
	harness := New(&inProcessAgentProcess{agent: &externalAgent{toolNameToCall: "note_write", toolArguments: map[string]any{"text": "회의록"}}}, newPublishedToolCatalog(t), taskRuns)
	if agentAuthorsItsLedger {
		harness.UseLedgerExchange(nil)
	}
	executed := []daemonExecutedTool{}
	if _, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{RequesterPersonID: "person-1"},
		ExistingTaskRunID: taskRunID,
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
		CarriedOutCalls:   carriedOutCalls,
	}); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return taskRuns.ListTaskEvent(taskRunID)
}

func toolEventsOf(taskEvents []agentcontract.TaskEvent) []agentcontract.TaskEvent {
	toolEvents := []agentcontract.TaskEvent{}
	for _, taskEvent := range taskEvents {
		if _, isRequest := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventRequestedSuffix); isRequest {
			toolEvents = append(toolEvents, taskEvent)
		}
		if _, isResult := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventResultSuffix); isResult {
			toolEvents = append(toolEvents, taskEvent)
		}
	}
	return toolEvents
}

func TestTheHostRecordsACatalogCallAnAgentWithoutALedgerMakes(t *testing.T) {
	toolEvents := toolEventsOf(runTurnCallingNoteWrite(t, false))

	if len(toolEvents) != 2 || toolEvents[0].Name != "tool.note_write.requested" || toolEvents[1].Name != "tool.note_write.result" {
		t.Fatalf("expected the host to record the call it executed for the agent, got %+v", toolEvents)
	}
	requested := agentcontract.ToolCallRecordOf[agentcontract.ToolCallRequested](toolEvents[0].Body)
	result := agentcontract.ToolCallRecordOf[agentcontract.ToolCallResult](toolEvents[1].Body)
	if requested.ObservationID != result.ObservationID || requested.SideEffectClass != toolcontract.ToolSideEffectStateChange || !result.Succeeded() {
		t.Fatalf("expected one succeeded observation saying what the call changes, got %+v and %+v", requested, result)
	}
}

func TestTheHostLeavesTheRecordToAnAgentThatAuthorsItsLedger(t *testing.T) {
	if toolEvents := toolEventsOf(runTurnCallingNoteWrite(t, true)); len(toolEvents) != 0 {
		t.Fatalf("expected an agent whose ledger the host replays to it to be the only author of its tool records, got %+v", toolEvents)
	}
}

func TestTheHostRecordsACallItCarriedOutAfterApprovalForAnAgentWithoutALedger(t *testing.T) {
	carriedOut := agentcontract.CarriedOutCall{ToolName: "note_write", ToolInput: json.RawMessage(`{"text":"승인된 회의록"}`), Result: toolcontract.ToolSuccess("저장했습니다")}

	withoutLedger := toolEventsOf(runTurnCallingNoteWrite(t, false, carriedOut))
	withLedger := toolEventsOf(runTurnCallingNoteWrite(t, true, carriedOut))

	if len(withoutLedger) != 4 || string(agentcontract.ToolCallRecordOf[agentcontract.ToolCallRequested](withoutLedger[0].Body).Input) != `{"text":"승인된 회의록"}` {
		t.Fatalf("expected the carried-out call to be recorded before the agent's own call, got %+v", withoutLedger)
	}
	if len(withLedger) != 0 {
		t.Fatalf("expected an agent that authors its ledger to record the carried-out call itself, got %+v", withLedger)
	}
}
