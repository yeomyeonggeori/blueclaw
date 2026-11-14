package acpharness

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const turnResultMetaKey = "example.com/turn-result"

func runTurnReturning(t *testing.T, carried agentcontract.AgentTurnResult) (agentcontract.AgentTurnResult, *taskstate.TaskRunService, string) {
	t.Helper()
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{promptResponseMeta: map[string]any{turnResultMetaKey: carried}}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns)
	harness.UseTurnResultMeta(turnResultMetaKey)
	executed := []daemonExecutedTool{}

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{
			RequesterPersonID: "person-1",
		},
		ExistingTaskRunID: taskRunID,
		ProfileName:       "default",
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return turnResult, taskRuns, taskRunID
}

func TestTheVerdictTheAgentReportsComesBackWithItsReplyAndLeavesTheStoreToTheHost(t *testing.T) {
	turnResult, taskRuns, taskRunID := runTurnReturning(t, agentcontract.AgentTurnResult{
		TaskRun:       agentcontract.TaskRun{Status: agentcontract.TaskStatusCompleted, Result: "노트를 남겼습니다"},
		FinishMessage: "노트를 남겼습니다",
		ToolNames:     []string{"note_write"},
	})

	storedTaskRun, _ := taskRuns.FindTaskRun(taskRunID)
	if turnResult.TaskRun.Status != agentcontract.TaskStatusCompleted || storedTaskRun.Status != agentcontract.TaskStatusPlanned {
		t.Fatalf("the adapter reports the agent's verdict and the host settles its run, got returned %q and stored %q", turnResult.TaskRun.Status, storedTaskRun.Status)
	}
	if turnResult.FinishMessage != "노트를 남겼습니다" || len(turnResult.ToolNames) != 1 {
		t.Fatalf("the reply and the tools the agent used come back as the loop would have returned them, got %+v", turnResult)
	}
}

func TestAFailedTurnTheAgentReportsComesBackWithItsReason(t *testing.T) {
	turnResult, _, _ := runTurnReturning(t, agentcontract.AgentTurnResult{
		TaskRun: agentcontract.TaskRun{Status: agentcontract.TaskStatusFailed, FailureReason: "the model gave up"},
	})

	if turnResult.TaskRun.Status != agentcontract.TaskStatusFailed || turnResult.TaskRun.FailureReason != "the model gave up" {
		t.Fatalf("expected the agent's failure and its reason, got %+v", turnResult.TaskRun)
	}
}

func TestAFileTheAgentReadIsNotAttachedToItsReply(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{
		toolNameToCall:     "file_deliver",
		toolArguments:      map[string]any{"path": "/workspace/read-only.png"},
		promptResponseMeta: map[string]any{turnResultMetaKey: agentcontract.AgentTurnResult{TaskRun: agentcontract.TaskRun{Status: agentcontract.TaskStatusCompleted}}},
	}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns)
	harness.UseTurnResultMeta(turnResultMetaKey)

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{
			RequesterPersonID: "person-1",
		},
		ExistingTaskRunID: taskRunID,
		Prompt:            "읽어줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           fileDeliverToolSet(t, []toolcontract.FileAttachment{{Filename: "read-only.png", DevicePath: "/workspace/read-only.png"}}),
	})

	if errorValue != nil || len(turnResult.Attachments) != 0 {
		t.Fatalf("what the loop attaches is what it delivered, and a file a tool read is not delivered, got %+v, %v", turnResult.Attachments, errorValue)
	}
}
