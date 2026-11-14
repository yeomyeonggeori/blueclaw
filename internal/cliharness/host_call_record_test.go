package cliharness

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestTheHostRecordsTheCallsItRunsForACommandLineAgent(t *testing.T) {
	taskRuns := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRun := taskRuns.CreateTaskRunWithOrigin("person-1", taskstate.TaskRunOrigin{}, "회의록 정리해줘")
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-token" })
	catalogServer := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(catalogServer.Close)
	publisher := &recordingSessionPublisher{endpoint: catalogServer.URL, resolver: resolver}
	harness := New(AgentCommand{Path: "/bin/echo", HarnessName: "test-harness"}, publisher, taskRuns)

	runEchoingTurn(t, harness, agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{RequesterPersonID: "person-1"},
		ExistingTaskRunID: taskRun.TaskRunID,
		Prompt:            "승인된 일 이어서 해줘",
		CarriedOutCalls:   []agentcontract.CarriedOutCall{{ToolName: "noop", ToolInput: json.RawMessage(`{}`), Result: toolcontract.ToolSuccess("했습니다")}},
	})

	if len(publisher.callLedgers) != 1 || publisher.callLedgers[0] != taskRuns {
		t.Fatalf("expected the catalog the agent calls through to record into the run's ledger, got %+v", publisher.callLedgers)
	}
	names := []string{}
	for _, taskEvent := range taskRuns.ListTaskEvent(taskRun.TaskRunID) {
		names = append(names, taskEvent.Name)
	}
	if !containsInOrder(names, "tool.noop.requested", "tool.noop.result") {
		t.Fatalf("expected the call carried out after approval to be recorded, got %v", names)
	}
}

func containsInOrder(names []string, wanted ...string) bool {
	next := 0
	for _, name := range names {
		if next < len(wanted) && name == wanted[next] {
			next++
		}
	}
	return next == len(wanted)
}
