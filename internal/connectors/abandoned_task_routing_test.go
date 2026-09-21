package connectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
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

func TestASupersededRunReclaimedBeforeTheCancelStillSaysItWasSuperseded(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	reclaimedTaskRun := seedAbandonedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "대체된 요청")
	connectorRuntime.taskRunService.AppendTaskEvent(reclaimedTaskRun.TaskRunID, agentcontract.TaskEventAgentTaskSource, agentruntime.MarshalBody(map[string]string{"sourceReference": "message-replaced"}))
	if _, isInterrupted := connectorRuntime.taskRunService.InterruptInactiveTaskRun(reclaimedTaskRun.TaskRunID, agentcontract.TaskInterruptReasonUnownedExecution); !isInterrupted {
		t.Fatal("expected the unowned run to be reclaimed")
	}

	connectorRuntime.cancelPendingSourceTask("person-1", "test", "direct-1", "message-replaced")

	if !connectorTaskEventsContain(connectorRuntime, reclaimedTaskRun.TaskRunID, agentcontract.TaskEventTaskSupersededByMessage, "message-replaced") {
		t.Fatal("the ledger has to keep the fact that the requester replaced this run")
	}
}

func TestASupersedeIsRecordedEvenWhenTheCancelTransitionFails(t *testing.T) {
	now := time.Now()
	taskRunRepository := newTestTaskRunRepository()
	runningTaskRun := task.TaskRun{
		TaskRunID:            "task-replaced",
		RequesterPersonID:    "person-1",
		OriginConversationID: "direct-1",
		CurrentAttemptID:     "attempt-replaced",
		Status:               task.TaskStatusRunning,
		Prompt:               "대체된 요청",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	taskRunRepository.taskRuns[runningTaskRun.TaskRunID] = runningTaskRun
	connectorRuntime, _, taskEventService, _ := newStubbedRepositoryBackedTestConnectorRuntime(t, taskRunRepository)
	taskEventService.AppendTaskEvent(runningTaskRun.TaskRunID, agentcontract.TaskEventAgentTaskSource, agentruntime.MarshalBody(map[string]string{"sourceReference": "message-replaced"}))
	taskRunRepository.transitionError = errors.New("the store refused the write")

	connectorRuntime.cancelPendingSourceTask("person-1", "test", "direct-1", "message-replaced")

	if storedTaskRun := taskRunRepository.taskRuns[runningTaskRun.TaskRunID]; storedTaskRun.Status != task.TaskStatusRunning {
		t.Fatalf("stored status = %s, want the cancel to have failed so the branch under test is reached", storedTaskRun.Status)
	}
	if !connectorTaskEventsContain(connectorRuntime, runningTaskRun.TaskRunID, agentcontract.TaskEventTaskSupersededByMessage, "message-replaced") {
		t.Fatal("the ledger has to keep the fact that the requester replaced this run even when the cancel could not land")
	}
}
