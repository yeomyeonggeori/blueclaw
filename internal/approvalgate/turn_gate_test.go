package approvalgate

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingApprovalGate struct {
	mutex    sync.Mutex
	outcome  mcpserver.ApprovalOutcome
	requests []mcpserver.ApprovalRequest
}

func (gate *recordingApprovalGate) ReviewToolCall(_ context.Context, toolInvocation toolcontract.ToolInvocation, toolDefinition toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	if !toolDefinition.RequiresApproval {
		return toolcontract.ToolCallReview{MayProceed: true}, nil
	}
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	gate.requests = append(gate.requests, mcpserver.ApprovalRequest{
		RequesterPersonID: "person-1",
		ToolName:          toolDefinition.Name,
		ToolInput:         toolInvocation.Input,
		ApprovalScope:     toolDefinition.ApprovalScope,
	})
	switch gate.outcome.Decision {
	case mcpserver.ApprovalDecisionApproved:
		return toolcontract.ToolCallReview{MayProceed: true}, nil
	case mcpserver.ApprovalDecisionRejected:
		return toolcontract.ToolCallReview{Result: rejectedCallResult(gate.outcome.Notice)}, nil
	}
	return toolcontract.ToolCallReview{Result: HeldCallResult(gate.outcome.Notice)}, nil
}

func (gate *recordingApprovalGate) received() []mcpserver.ApprovalRequest {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	return append([]mcpserver.ApprovalRequest{}, gate.requests...)
}

func approvalToolSet(t *testing.T, executed *[]string) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"file_delete", "file_read"})
	toolSet.AllowTestReplacement()
	register := func(name string, requiresApproval bool, approvalScope string) {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID:               "test:" + name,
			Name:             name,
			Description:      name,
			Visibility:       toolcontract.ToolVisibilityModel,
			InputSchema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			RequiresApproval: requiresApproval,
			ApprovalScope:    approvalScope,
			SideEffectClass:  toolcontract.ToolSideEffectStateChange,
			ResultContract:   &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
		}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			*executed = append(*executed, invocation.ToolName)
			return toolcontract.ToolSuccessData("done", json.RawMessage(`{}`)), nil
		})
		if errorValue != nil {
			t.Fatalf("expected %s to register: %v", name, errorValue)
		}
	}
	register("file_delete", true, "workspace_files")
	register("file_read", false, "")
	return toolSet
}

func invokeThroughGate(t *testing.T, toolCallGate toolcontract.ToolCallGate, toolName string) (*[]string, toolcontract.ToolResult) {
	t.Helper()
	return invokeThroughGateInContext(t, context.Background(), toolCallGate, toolName)
}

func invokeThroughGateInContext(t *testing.T, invocationContext context.Context, toolCallGate toolcontract.ToolCallGate, toolName string) (*[]string, toolcontract.ToolResult) {
	t.Helper()
	executed := []string{}
	toolSet := approvalToolSet(t, &executed)
	toolSet.UseToolCallGate(toolCallGate)
	result, errorValue := toolSet.Invoke(invocationContext, toolcontract.ToolInvocation{
		ToolName: toolName,
		Input:    json.RawMessage(`{"path":"~/notes.md"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected the call to reach the tool set: %v", errorValue)
	}
	return &executed, result
}

func TestAToolNeedingApprovalDoesNotRunWithoutAGate(t *testing.T) {
	var missingGate *Gate

	executed, result := invokeThroughGate(t, missingGate.TurnGate(TurnContext{RequesterPersonID: "person-1"}), "file_delete")

	if len(*executed) != 0 {
		t.Fatalf("expected an approval-gated tool to refuse to run with no gate configured, it ran %+v", *executed)
	}
	if !result.Failed() {
		t.Fatalf("expected the agent to be told the call did not run, got %+v", result)
	}
}

func TestAToolNeedingApprovalRunsOnlyOnceApproved(t *testing.T) {
	gate := &recordingApprovalGate{outcome: mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionApproved}}

	executed, result := invokeThroughGate(t, gate, "file_delete")

	if len(*executed) != 1 || (*executed)[0] != "file_delete" {
		t.Fatalf("expected an approved call to run once, got %+v", *executed)
	}
	if result.Failed() {
		t.Fatalf("expected an approved call to succeed, got %+v", result)
	}
	received := gate.received()
	if len(received) != 1 || received[0].ApprovalScope != "workspace_files" {
		t.Fatalf("expected the gate to be asked about this scope, got %+v", received)
	}
	if !strings.Contains(string(received[0].ToolInput), "notes.md") {
		t.Fatalf("expected the gate to see the exact call it is approving, got %s", received[0].ToolInput)
	}
}

func TestAHeldOrRejectedCallNeverRuns(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		decision mcpserver.ApprovalDecision
	}{
		{name: "held", decision: mcpserver.ApprovalDecisionHeld},
		{name: "rejected", decision: mcpserver.ApprovalDecisionRejected},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			gate := &recordingApprovalGate{outcome: mcpserver.ApprovalOutcome{Decision: testCase.decision, Notice: "요청자 확인이 필요합니다"}}

			executed, result := invokeThroughGate(t, gate, "file_delete")

			if len(*executed) != 0 {
				t.Fatalf("expected a %s call never to run, it ran %+v", testCase.name, *executed)
			}
			if !result.Failed() || !strings.Contains(result.UserSafeFailureSummary(), "요청자 확인이 필요합니다") {
				t.Fatalf("expected the gate's own wording to reach the agent, got %+v", result)
			}
		})
	}
}

func TestAToolThatNeedsNoApprovalNeverReachesTheGate(t *testing.T) {
	gate := &recordingApprovalGate{outcome: mcpserver.ApprovalOutcome{Decision: mcpserver.ApprovalDecisionHeld}}

	executed, result := invokeThroughGate(t, gate, "file_read")

	if len(*executed) != 1 || result.Failed() {
		t.Fatalf("expected an ungated tool to run, got executed=%+v result=%+v", *executed, result)
	}
	if len(gate.received()) != 0 {
		t.Fatalf("expected the gate not to be consulted for a tool that needs no approval, got %+v", gate.received())
	}
}

func TestADelegatedTurnIsDeniedRatherThanHeld(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	delegatedContext := toolcontract.WithDelegatedTurn(toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID))

	executed, result := invokeThroughGateInContext(t, delegatedContext, gate.TurnGate(TurnContext{RequesterPersonID: "person-1", ConversationID: "conversation-1"}), "file_delete")

	if len(*executed) != 0 {
		t.Fatalf("expected a denied call never to run, it ran %+v", *executed)
	}
	if !result.Failed() || result.Failure.RequiresApproval {
		t.Fatalf("a call the child must stop waiting for is not a call that is still held: %+v", result)
	}
	currentTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if currentTaskRun.Status == task.TaskStatusWaitingApproval {
		t.Fatal("a run left waiting for an approval nobody was asked for is a run a later approve can hijack")
	}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRun.TaskRunID) {
		if taskEvent.Name == "approval.pending_call" {
			t.Fatalf("a denied call is not a held call, and the ledger must not offer one to resume: %s", taskEvent.Body)
		}
	}
}

func TestATopLevelTurnStillHoldsTheCallItNeedsApprovalFor(t *testing.T) {
	gate, taskRunService, taskRun := gateFixture(t)
	turnContext := toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID)

	executed, result := invokeThroughGateInContext(t, turnContext, gate.TurnGate(TurnContext{RequesterPersonID: "person-1", ConversationID: "conversation-1"}), "file_delete")

	if len(*executed) != 0 {
		t.Fatalf("expected a held call never to run, it ran %+v", *executed)
	}
	if !result.Failed() || !result.Failure.RequiresApproval {
		t.Fatalf("the turn the requester is talking to is the one that may ask them: %+v", result)
	}
	heldTaskRun, _ := taskRunService.FindTaskRun(taskRun.TaskRunID)
	if heldTaskRun.Status != task.TaskStatusWaitingApproval {
		t.Fatalf("expected the run to wait for the requester, got %q", heldTaskRun.Status)
	}
}
