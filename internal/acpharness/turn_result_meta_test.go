package acpharness

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
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
		RequesterPersonID: "person-1",
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

func TestACompletedTurnTheAgentReportsCompletesTheHostsRunWithItsReply(t *testing.T) {
	turnResult, taskRuns, taskRunID := runTurnReturning(t, agentcontract.AgentTurnResult{
		TaskRun:       agentcontract.TaskRun{Status: agentcontract.TaskStatusCompleted, Result: "노트를 남겼습니다"},
		FinishMessage: "노트를 남겼습니다",
		ToolNames:     []string{"note_write"},
	})

	storedTaskRun, _ := taskRuns.FindTaskRun(taskRunID)
	if storedTaskRun.Status != agentcontract.TaskStatusCompleted || turnResult.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("the host owns the run, so the agent's verdict has to land on it, got stored %q and returned %q", storedTaskRun.Status, turnResult.TaskRun.Status)
	}
	if turnResult.FinishMessage != "노트를 남겼습니다" || len(turnResult.ToolNames) != 1 {
		t.Fatalf("the reply and the tools the agent used come back as the loop would have returned them, got %+v", turnResult)
	}
}

func TestAFailedTurnTheAgentReportsFailsTheHostsRunWithItsReason(t *testing.T) {
	_, taskRuns, taskRunID := runTurnReturning(t, agentcontract.AgentTurnResult{
		TaskRun: agentcontract.TaskRun{Status: agentcontract.TaskStatusFailed, FailureReason: "the model gave up"},
	})

	storedTaskRun, _ := taskRuns.FindTaskRun(taskRunID)
	if storedTaskRun.Status != agentcontract.TaskStatusFailed || storedTaskRun.FailureReason != "the model gave up" {
		t.Fatalf("expected the run failed with the agent's reason, got %+v", storedTaskRun)
	}
}

func TestATurnThatBeginsMovesTheHostsRunOutOfPlanned(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns)
	harness.UseTurnResultMeta(turnResultMetaKey)
	executed := []daemonExecutedTool{}

	harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRunID,
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})

	storedTaskRun, _ := taskRuns.FindTaskRun(taskRunID)
	if storedTaskRun.Status != agentcontract.TaskStatusRunning {
		t.Fatalf("an agent that reports no verdict leaves the run running, got %q", storedTaskRun.Status)
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
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRunID,
		Prompt:            "읽어줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           fileDeliverToolSet(t, []toolcontract.FileAttachment{{Filename: "read-only.png", DevicePath: "/workspace/read-only.png"}}),
	})

	if errorValue != nil || len(turnResult.Attachments) != 0 {
		t.Fatalf("what the loop attaches is what it delivered, and a file a tool read is not delivered, got %+v, %v", turnResult.Attachments, errorValue)
	}
}

func TestATurnTheStoreHoldsParkedReturnsParkedWhateverTheAgentReports(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{cancelled: make(chan struct{}, 4), promptResponseMeta: map[string]any{turnResultMetaKey: agentcontract.AgentTurnResult{
		TaskRun: agentcontract.TaskRun{Status: agentcontract.TaskStatusCompleted, Result: "done"},
	}}}
	agent.promptScripts = []func(context.Context, acp.PromptRequest) acp.StopReason{func(context.Context, acp.PromptRequest) acp.StopReason {
		taskRuns.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, "approve the send?")
		return acp.StopReasonEndTurn
	}}
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns)
	harness.UseTurnResultMeta(turnResultMetaKey)
	executed := []daemonExecutedTool{}

	turnResult, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRunID,
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	if turnResult.TaskRun.Status != agentcontract.TaskStatusWaitingApproval || turnResult.UserNotice != "approve the send?" {
		t.Fatalf("the store says the run waits, so the agent's own verdict does not stand, got %q and %q", turnResult.TaskRun.Status, turnResult.UserNotice)
	}
}
