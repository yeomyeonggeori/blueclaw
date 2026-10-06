package acpharness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const waitForTheAgent = 5 * time.Second

func toolCallEnded() acp.SessionUpdate {
	return acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted))
}

func sendUpdate(ctx context.Context, agent *externalAgent, request acp.PromptRequest, update acp.SessionUpdate) {
	_ = agent.connection.SessionUpdate(ctx, acp.SessionNotification{SessionId: request.SessionId, Update: update})
}

func awaitSignal(t *testing.T, signals chan struct{}) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(waitForTheAgent):
		t.Fatal("the agent was never told")
	}
}

func runControlledTurn(t *testing.T, agent *externalAgent, taskRuns *taskstate.TaskRunService, taskRunID string, configure func(*Harness)) agentcontract.AgentTurnResult {
	t.Helper()
	harness := New(&inProcessAgentProcess{agent: agent}, newPublishedToolCatalog(t), taskRuns)
	configure(harness)
	executed := []daemonExecutedTool{}
	ctx := toolcallprogress.WithObserver(context.Background(), func(acp.SessionUpdate) {})
	result, errorValue := harness.RunTurn(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRunID,
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to end: %v", errorValue)
	}
	return result
}

func parkTheRun(taskRuns *taskstate.TaskRunService, taskRunID string) {
	taskRuns.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, "approve the send?")
	taskRuns.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, `{"kind":"ask_confirm"}`)
}

func TestAPauseInTheMiddleOfAToolCallEndsThePromptWithTheStatusTheStoreHolds(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{cancelled: make(chan struct{}, 4)}
	wasCancelled := false
	agent.promptScripts = []func(context.Context, acp.PromptRequest) acp.StopReason{func(ctx context.Context, request acp.PromptRequest) acp.StopReason {
		parkTheRun(taskRuns, taskRunID)
		sendUpdate(ctx, agent, request, toolCallEnded())
		select {
		case <-agent.cancelled:
			wasCancelled = true
			return acp.StopReasonCancelled
		case <-time.After(waitForTheAgent):
			return acp.StopReasonEndTurn
		}
	}}

	result := runControlledTurn(t, agent, taskRuns, taskRunID, func(*Harness) {})

	if !wasCancelled {
		t.Fatal("the agent was never told to stop")
	}
	if result.UserNotice != "approve the send?" {
		t.Fatalf("a parked turn carries the question the store holds, got %q", result.UserNotice)
	}

	if result.TaskRun.Status != agentcontract.TaskStatusWaitingApproval {
		t.Fatalf("a run parked in the middle of the agent's turn has to end the turn parked, got %q", result.TaskRun.Status)
	}
	if len(agent.promptTexts) != 1 {
		t.Fatalf("a parked run is not prompted again, got %v", agent.promptTexts)
	}
}

func TestARunThatResumesBeforeTheToolCallEndsIsNotCancelled(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{cancelled: make(chan struct{}, 4)}
	wasCancelled := false
	agent.promptScripts = []func(context.Context, acp.PromptRequest) acp.StopReason{func(ctx context.Context, request acp.PromptRequest) acp.StopReason {
		parkTheRun(taskRuns, taskRunID)
		time.Sleep(100 * time.Millisecond)
		taskRuns.ResumeTaskRun(taskRunID)
		sendUpdate(ctx, agent, request, toolCallEnded())
		select {
		case <-agent.cancelled:
			wasCancelled = true
		case <-time.After(300 * time.Millisecond):
		}
		return acp.StopReasonEndTurn
	}}

	result := runControlledTurn(t, agent, taskRuns, taskRunID, func(*Harness) {})

	if wasCancelled || result.TaskRun.Status == agentcontract.TaskStatusWaitingApproval {
		t.Fatalf("a run answered while the call waited goes on, got status %q and cancelled=%v", result.TaskRun.Status, wasCancelled)
	}
}

func steerEventBody() string {
	body, _ := json.Marshal(map[string]string{"instruction": "make it shorter", "reason": "revision", "messageID": "message-2"})
	return string(body)
}

func TestASteerReachesABluecollarAgentThroughItsOwnMethod(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{cancelled: make(chan struct{}, 4)}
	agent.promptScripts = []func(context.Context, acp.PromptRequest) acp.StopReason{func(context.Context, acp.PromptRequest) acp.StopReason {
		taskRuns.AppendTaskEvent(taskRunID, agentcontract.TaskEventTaskSteerRequested, steerEventBody())
		awaitSignal(t, agent.cancelled)
		return acp.StopReasonEndTurn
	}}

	runControlledTurn(t, agent, taskRuns, taskRunID, func(harness *Harness) {
		harness.UseSteerExtension("_bluecollar.dev/steer", func(sessionID acp.SessionId, steer SteerRequest) any {
			return map[string]string{"instruction": steer.Instruction}
		})
	})

	if len(agent.extensionMethods) != 1 || agent.extensionMethods[0] != "_bluecollar.dev/steer" || string(agent.extensionParams[0]) != `{"instruction":"make it shorter"}` {
		t.Fatalf("the steer has to travel on the agent's own method, got %v %s", agent.extensionMethods, agent.extensionParams)
	}
	if len(agent.promptTexts) != 1 {
		t.Fatalf("an agent that takes the steer is not interrupted, got %v", agent.promptTexts)
	}
}

func TestASteerReachesAnyOtherAgentAsACancelAndANewPrompt(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{cancelled: make(chan struct{}, 4)}
	agent.promptScripts = []func(context.Context, acp.PromptRequest) acp.StopReason{
		func(context.Context, acp.PromptRequest) acp.StopReason {
			taskRuns.AppendTaskEvent(taskRunID, agentcontract.TaskEventTaskSteerRequested, steerEventBody())
			awaitSignal(t, agent.cancelled)
			return acp.StopReasonCancelled
		},
		func(context.Context, acp.PromptRequest) acp.StopReason { return acp.StopReasonEndTurn },
	}

	runControlledTurn(t, agent, taskRuns, taskRunID, func(*Harness) {})

	if len(agent.promptTexts) != 2 || !strings.Contains(agent.promptTexts[1], "make it shorter") {
		t.Fatalf("a steer has to arrive as the next prompt of the same session, got %v", agent.promptTexts)
	}
}

func TestASteerThatArrivesAfterTheRunParkedIsNotPromptedIn(t *testing.T) {
	taskRuns, taskRunID := newTaskRunStore()
	agent := &externalAgent{cancelled: make(chan struct{}, 4)}
	agent.promptScripts = []func(context.Context, acp.PromptRequest) acp.StopReason{
		func(ctx context.Context, request acp.PromptRequest) acp.StopReason {
			parkTheRun(taskRuns, taskRunID)
			taskRuns.AppendTaskEvent(taskRunID, agentcontract.TaskEventTaskSteerRequested, steerEventBody())
			sendUpdate(ctx, agent, request, toolCallEnded())
			awaitSignal(t, agent.cancelled)
			return acp.StopReasonCancelled
		},
		func(context.Context, acp.PromptRequest) acp.StopReason { return acp.StopReasonEndTurn },
	}

	result := runControlledTurn(t, agent, taskRuns, taskRunID, func(*Harness) {})

	if len(agent.promptTexts) != 1 || result.TaskRun.Status != agentcontract.TaskStatusWaitingApproval {
		t.Fatalf("a parked run waits for its answer, not for a steer, got %v and %q", agent.promptTexts, result.TaskRun.Status)
	}
}

func publishedTurnContext(t *testing.T, configure func(*Harness)) context.Context {
	t.Helper()
	taskRuns, taskRunID := newTaskRunStore()
	catalog := newPublishedToolCatalog(t)
	harness := New(&inProcessAgentProcess{agent: &externalAgent{}}, catalog, taskRuns)
	configure(harness)
	executed := []daemonExecutedTool{}
	_, errorValue := harness.RunTurn(context.Background(), agentcontract.AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRunID,
		Prompt:            "회의록 정리해줘",
		WorkspaceRootPath: t.TempDir(),
		ToolSet:           requesterToolSet(t, "person-1", &executed),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return catalog.publishedToolSet.TurnContext
}

func TestAHarnessThatAsksForItHandsItsTurnToTheToolCatalog(t *testing.T) {
	if publishedTurnContext(t, func(harness *Harness) { harness.UseTurnContextOnToolCalls() }) == nil {
		t.Fatal("the tool calls of this harness run inside its turn")
	}
	if publishedTurnContext(t, func(*Harness) {}) != nil {
		t.Fatal("every other harness keeps the tool calls out of its turn")
	}
}
