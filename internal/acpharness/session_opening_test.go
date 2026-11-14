package acpharness

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func TestAnAgentThatCanReopenASessionContinuesTheOneTheLedgerRecorded(t *testing.T) {
	for _, reopening := range []string{"resume", "load"} {
		taskRuns := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
		taskRun := taskRuns.CreateTaskRunWithOrigin("person-1", taskstate.TaskRunOrigin{}, "회의록 정리해줘")
		agent := &externalAgent{reopensSessionsWith: reopening}
		executed := []daemonExecutedTool{}
		request := agentcontract.AgentTurnRequest{
			RequesterIdentity: agentcontract.RequesterIdentity{RequesterPersonID: "person-1"},
			ExistingTaskRunID: taskRun.TaskRunID,
			Prompt:            "회의록 정리해줘",
			WorkspaceRootPath: t.TempDir(),
			ToolSet:           requesterToolSet(t, "person-1", &executed),
		}

		New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns).RunTurn(context.Background(), request)
		request.IsRuntimeRestartResume = true
		afterRestart, errorValue := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns).RunTurn(context.Background(), request)
		if errorValue != nil {
			t.Fatalf("%s: expected the second turn to run: %v", reopening, errorValue)
		}

		if agent.newSessionCount != 1 || len(agent.reopenedSessionIDs) != 1 || agent.reopenedSessionIDs[0] != "session-1" {
			t.Fatalf("%s: a turn after a restart continues the agent's conversation, got %d new sessions and reopened %v", reopening, agent.newSessionCount, agent.reopenedSessionIDs)
		}
		if strings.Contains(afterRestart.FinishMessage, "지난 턴에 이미 한 답") {
			t.Fatalf("%s: an agent replaying its history is not answering this turn, got %q", reopening, afterRestart.FinishMessage)
		}
	}
}

func TestAnAgentThatCannotReopenASessionStartsAFreshOne(t *testing.T) {
	taskRuns := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRun := taskRuns.CreateTaskRunWithOrigin("person-1", taskstate.TaskRunOrigin{}, "회의록 정리해줘")
	agent := &externalAgent{}
	executed := []daemonExecutedTool{}
	request := agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{RequesterPersonID: "person-1"},
		ExistingTaskRunID: taskRun.TaskRunID,
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	}

	New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns).RunTurn(context.Background(), request)
	New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns).RunTurn(context.Background(), request)

	if agent.newSessionCount != 2 || len(agent.reopenedSessionIDs) != 0 {
		t.Fatalf("an agent that never said it can reopen a session is asked for a new one, got %d new and %v reopened", agent.newSessionCount, agent.reopenedSessionIDs)
	}
}
