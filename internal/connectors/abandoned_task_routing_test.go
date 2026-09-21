package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestAMessageIsNotRoutedIntoARunningTaskNoTurnIsWorkingOn(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	busyCapableDecision := startTaskTurnDecision()
	busyCapableDecision.BusyRoute = agentcontract.BusyRouteStatus
	harness.TurnDecision = busyCapableDecision
	harness.Reply = "아직 그 작업을 하고 있습니다."
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "새 작업으로 처리했습니다."}
	abandonedTaskRun := seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "멈춘 작업")
	event := testInboundEvent("message-after-abandoned-task")
	event.Prompt = "이거 어떻게 됐어?"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected the message after an abandoned task to process: %v", errorValue)
	}
	if result.TaskRunID == "" || result.TaskRunID == abandonedTaskRun.TaskRunID {
		t.Fatalf("expected a new task run instead of the abandoned one, got %+v", result)
	}
	storedTaskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(abandonedTaskRun.TaskRunID)
	if !isFound || storedTaskRun.Status != task.TaskStatusInterrupted {
		t.Fatalf("expected the abandoned task interrupted, found=%v task=%+v", isFound, storedTaskRun)
	}
	if connectorTaskEventsContain(connectorRuntime, abandonedTaskRun.TaskRunID, "task.busy_message.routed", event.MessageID) {
		t.Fatal("a run nothing is working on must not swallow the message")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "새 작업으로 처리했습니다." {
		t.Fatalf("expected the requester to be answered, got %+v", adapter.sentReplies)
	}
}
