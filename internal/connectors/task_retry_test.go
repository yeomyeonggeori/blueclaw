package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type retryQueueRepository struct {
	*testConnectorQueueRepository
	enqueueError error
	enqueuedKeys map[string]bool
}

func newRetryQueueRepository() *retryQueueRepository {
	return &retryQueueRepository{testConnectorQueueRepository: &testConnectorQueueRepository{}, enqueuedKeys: map[string]bool{}}
}

func (repository *retryQueueRepository) TryEnqueueConnectorEvent(event PlatformInboundEvent) (bool, ConnectorRuntimeResult, error) {
	if repository.enqueueError != nil {
		return false, ConnectorRuntimeResult{}, repository.enqueueError
	}
	if repository.enqueuedKeys[event.DedupeKey()] {
		return true, ConnectorRuntimeResult{Handled: true, Duplicate: true, Platform: event.Platform}, nil
	}
	repository.enqueuedKeys[event.DedupeKey()] = true
	return repository.testConnectorQueueRepository.TryEnqueueConnectorEvent(event)
}

func TestRetryTaskRunQueuesOneChildAndPreservesSource(t *testing.T) {
	connectorRuntime, _, harness := newStubbedTestConnectorRuntime(t)
	queueRepository := newRetryQueueRepository()
	connectorRuntime.UseEventRepository(queueRepository)
	sourceTaskRun := seedRetrySourceTaskRun(t, connectorRuntime)
	originalSource, _ := connectorRuntime.taskRunService.FindTaskRun(sourceTaskRun.TaskRunID)

	childTaskRun, errorValue := connectorRuntime.RetryTaskRun(context.Background(), sourceTaskRun.TaskRunID)
	if errorValue != nil {
		t.Fatalf("retry failed: %v", errorValue)
	}
	if len(queueRepository.pendingEvents) != 1 {
		t.Fatalf("expected one queued retry, got %d", len(queueRepository.pendingEvents))
	}
	repeatedTaskRun, errorValue := connectorRuntime.RetryTaskRun(context.Background(), sourceTaskRun.TaskRunID)
	if errorValue != nil || repeatedTaskRun.TaskRunID != childTaskRun.TaskRunID {
		t.Fatalf("expected repeated retry to reuse child, got %+v, %v", repeatedTaskRun, errorValue)
	}
	if len(queueRepository.pendingEvents) != 1 {
		t.Fatalf("expected one queued retry after repeat, got %d", len(queueRepository.pendingEvents))
	}

	processOneRetryEvent(t, connectorRuntime, queueRepository)
	request := harness.LastTurnRequest()
	if request.ExistingTaskRunID != childTaskRun.TaskRunID || !request.IsTaskRunOpenedForThisTurn {
		t.Fatalf("retry did not use reserved child: %+v", request)
	}
	if request.IsApprovalContinuation || request.IsRuntimeRestartResume {
		t.Fatalf("retry inherited continuation state: %+v", request)
	}
	if request.OriginReplyTargetID != sourceTaskRun.OriginReplyTargetID || !request.OriginIsThread {
		t.Fatalf("retry lost origin context: %+v", request)
	}
	if request.PriorTask.TaskRunID != sourceTaskRun.TaskRunID {
		t.Fatalf("retry lost prior task context: %+v", request.PriorTask)
	}
	if len(request.PriorTask.RecordedAttempts) != 1 || len(request.PriorTask.RecordedAttempts[0].Effects) != 1 || request.PriorTask.RecordedAttempts[0].Effects[0].ID != "task-1" {
		t.Fatalf("retry lost the effect already recorded by the failed attempt: %+v", request.PriorTask)
	}
	updatedSource, isFound := connectorRuntime.taskRunService.FindTaskRun(sourceTaskRun.TaskRunID)
	if !isFound || updatedSource.Status != originalSource.Status || updatedSource.Result != originalSource.Result || updatedSource.FailureReason != originalSource.FailureReason {
		t.Fatalf("retry changed source task: before=%+v after=%+v", originalSource, updatedSource)
	}
}

func TestRetryTaskRunRejectsNonFailedTaskAndUnavailableRuntime(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	queueRepository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(queueRepository)
	plannedTaskRun := connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-1"}, "planned")
	if _, errorValue := connectorRuntime.RetryTaskRun(context.Background(), plannedTaskRun.TaskRunID); errorValue != ErrTaskRetryConflict {
		t.Fatalf("expected nonfailed conflict, got %v", errorValue)
	}
	if len(queueRepository.pendingEvents) != 0 {
		t.Fatal("nonfailed retry queued an event")
	}

	unavailableRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	failedTaskRun := seedRetrySourceTaskRun(t, unavailableRuntime)
	if _, errorValue := unavailableRuntime.RetryTaskRun(context.Background(), failedTaskRun.TaskRunID); errorValue != ErrTaskRetryUnavailable {
		t.Fatalf("expected unavailable retry, got %v", errorValue)
	}
	if len(unavailableRuntime.taskRunService.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatal("unavailable retry created a child")
	}
}

func TestRetryTaskRunRepairsEnqueueAfterTransientFailure(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	queueRepository := newRetryQueueRepository()
	connectorRuntime.UseEventRepository(queueRepository)
	sourceTaskRun := seedRetrySourceTaskRun(t, connectorRuntime)
	queueRepository.enqueueError = errors.New("queue unavailable")
	childTaskRun, errorValue := connectorRuntime.RetryTaskRun(context.Background(), sourceTaskRun.TaskRunID)
	if errorValue == nil || childTaskRun.TaskRunID == "" {
		t.Fatalf("expected enqueue failure with reserved child, child=%+v error=%v", childTaskRun, errorValue)
	}
	queueRepository.enqueueError = nil
	repairedTaskRun, errorValue := connectorRuntime.RetryTaskRun(context.Background(), sourceTaskRun.TaskRunID)
	if errorValue != nil || repairedTaskRun.TaskRunID != childTaskRun.TaskRunID {
		t.Fatalf("expected same child to be repaired, got %+v error=%v", repairedTaskRun, errorValue)
	}
	if len(queueRepository.pendingEvents) != 1 {
		t.Fatalf("expected one repaired queue event, got %d", len(queueRepository.pendingEvents))
	}
}

func TestRetryTaskRunResumesInterruptedChildWithoutDuplicateQueueLaunch(t *testing.T) {
	connectorRuntime, _, harness := newStubbedTestConnectorRuntime(t)
	queueRepository := newRetryQueueRepository()
	connectorRuntime.UseEventRepository(queueRepository)
	sourceTaskRun := seedRetrySourceTaskRun(t, connectorRuntime)
	childTaskRun, errorValue := connectorRuntime.RetryTaskRun(context.Background(), sourceTaskRun.TaskRunID)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, isInterrupted := connectorRuntime.taskRunService.InterruptInactiveTaskRun(childTaskRun.TaskRunID, task.TaskInterruptReasonRuntimeRestart); !isInterrupted {
		t.Fatal("expected retry child to become interrupted")
	}
	interruptedTaskRun, _ := connectorRuntime.taskRunService.FindTaskRun(childTaskRun.TaskRunID)
	if !connectorRuntime.CanResumeInterruptedTaskRun(interruptedTaskRun) {
		t.Fatal("expected interrupted retry to be resumable")
	}
	if _, errorValue = connectorRuntime.ResumeInterruptedTaskRun(context.Background(), interruptedTaskRun); errorValue != nil {
		t.Fatal(errorValue)
	}
	processOneRetryEvent(t, connectorRuntime, queueRepository)
	if harness.RunTurnCallCount() != 1 {
		t.Fatalf("expected one launch after resume and queued event, got %d", harness.RunTurnCallCount())
	}
}

func TestRetryTaskRunRejectsForgedChildReference(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	sourceTaskRun := seedRetrySourceTaskRun(t, connectorRuntime)
	childTaskRun := connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-1"}, sourceTaskRun.Prompt)
	event := retryInboundEvent(sourceTaskRun, childTaskRun, interruptedTaskLaunchContext{Platform: adapter.Name(), ConversationID: "direct-1", ReplyTargetID: "reply-1"})
	_, errorValue := connectorRuntime.processTaskRetry(context.Background(), adapter, event, adapter.SendReply)
	if errorValue != ErrTaskRetryUnavailable {
		t.Fatalf("expected forged child reference rejection, got %v", errorValue)
	}
	if harness.RunTurnCallCount() != 0 {
		t.Fatal("forged retry launched a task")
	}
}

type interruptedRetryHarness struct {
	*harnesstest.Harness
	testing        *testing.T
	taskRunService *task.TaskRunService
	isFirstTurn    bool
}

func (harness *interruptedRetryHarness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if !harness.isFirstTurn {
		return harness.Harness.RunTurn(ctx, request)
	}
	harness.isFirstTurn = false
	taskRun, _ := harness.taskRunService.FindTaskRun(request.ExistingTaskRunID)
	if _, isFound := interruptedTaskLaunchContextFromEvents(taskRun, harness.taskRunService.ListTaskEvent(taskRun.TaskRunID)); !isFound {
		harness.testing.Fatal("retry has no durable launch context before its first turn")
	}
	harness.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentGoalUpdated, `{"goalID":"retry-goal","currentObjective":"finish the remaining export"}`)
	interruptedTaskRun, _ := harness.taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, task.TaskInterruptReasonRuntimeRestart)
	return agentcontract.AgentTurnResult{TaskRun: interruptedTaskRun}, nil
}

func TestRetryTaskRunResumesStartedChildThroughRuntimeRecovery(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	baseHarness := harnesstest.New(taskRunService)
	harness := &interruptedRetryHarness{Harness: baseHarness, testing: t, taskRunService: taskRunService, isFirstTurn: true}
	connectorRuntime, adapter := connectorRuntimeForHarness(t, harness, baseHarness, baseHarness, baseHarness, taskRunService, testLanguageModel{reply: "stub"})
	queueRepository := newRetryQueueRepository()
	connectorRuntime.UseEventRepository(queueRepository)
	sourceTaskRun := seedRetrySourceTaskRun(t, connectorRuntime)
	childTaskRun, errorValue := connectorRuntime.RetryTaskRun(context.Background(), sourceTaskRun.TaskRunID)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	processOneRetryEvent(t, connectorRuntime, queueRepository)
	childTaskRun, _ = taskRunService.FindTaskRun(childTaskRun.TaskRunID)
	event := retryInboundEvent(sourceTaskRun, childTaskRun, interruptedTaskLaunchContext{Platform: adapter.Name()})
	result, errorValue := connectorRuntime.processTaskRetry(context.Background(), adapter, event, adapter.SendReply)
	if errorValue != nil || !result.Duplicate || baseHarness.RunTurnCallCount() != 0 {
		t.Fatalf("queued retry competed with runtime recovery: %+v, %v", result, errorValue)
	}
	if _, errorValue = connectorRuntime.ResumeInterruptedTaskRun(context.Background(), childTaskRun); errorValue != nil {
		t.Fatal(errorValue)
	}
	request := baseHarness.LastTurnRequest()
	if !request.IsRuntimeRestartResume || request.ExistingTaskRunID != childTaskRun.TaskRunID || request.ActiveGoal.CurrentObjective != "finish the remaining export" {
		t.Fatalf("retry did not restore child execution state: %+v", request)
	}
}

func seedRetrySourceTaskRun(t *testing.T, connectorRuntime *ConnectorRuntime) task.TaskRun {
	t.Helper()
	sourceTaskRun := connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{
		ConversationID: "direct-1", ReplyTargetID: "reply-1", IsThread: true,
	}, "recover the recorded task")
	launchEvent, errorValue := json.Marshal(interruptedTaskLaunchContext{
		SourceReference: "test:direct-1:source-message", Platform: "test", ProfileName: "default",
		RequesterPersonID: "person-1", ConversationID: "direct-1", ReplyTargetID: "reply-1", IsThread: true,
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	connectorRuntime.taskRunService.AppendTaskEvent(sourceTaskRun.TaskRunID, agentcontract.TaskEventAgentTaskLaunched, string(launchEvent))
	connectorRuntime.taskRunService.AppendTaskEvent(sourceTaskRun.TaskRunID, "tool.record.result", `{"observationID":"obs-001","toolInput":{"title":"sample task"},"effects":[{"objectType":"task","effect":"created","id":"task-1"}]}`)
	if _, errorValue = connectorRuntime.taskRunService.FailTaskRun(sourceTaskRun.TaskRunID, "recorded task failed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	return sourceTaskRun
}

func processOneRetryEvent(t *testing.T, connectorRuntime *ConnectorRuntime, queueRepository interface {
	ClaimPendingConnectorEvents(int, time.Duration) ([]QueuedConnectorEvent, error)
}) {
	t.Helper()
	queuedEvents, errorValue := queueRepository.ClaimPendingConnectorEvents(1, 0)
	if errorValue != nil || len(queuedEvents) != 1 {
		t.Fatalf("expected queued retry event: %v", errorValue)
	}
	connectorRuntime.processQueuedConnectorEvent(context.Background(), queuedEvents[0])
}
