package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

var (
	ErrTaskRetryConflict    = errors.New("task run is not retryable")
	ErrTaskRetryUnavailable = errors.New("task retry is unavailable")
)

const (
	taskRetryRequestedEvent = "task.retry_requested"
	taskRetrySourceEvent    = "task.retry_source"
)

func (connectorRuntime *ConnectorRuntime) RetryTaskRun(ctx context.Context, sourceTaskRunID string) (task.TaskRun, error) {
	connectorRuntime.retryMutex.Lock()
	defer connectorRuntime.retryMutex.Unlock()
	if ctx == nil || ctx.Err() != nil {
		return task.TaskRun{}, context.Canceled
	}
	if connectorRuntime.taskIntakeGate != nil && connectorRuntime.taskIntakeGate.IsQuiesced() {
		return task.TaskRun{}, ErrTaskRetryUnavailable
	}
	if connectorRuntime.queueRepository() == nil || connectorRuntime.identityService == nil {
		return task.TaskRun{}, ErrTaskRetryUnavailable
	}

	sourceTaskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(strings.TrimSpace(sourceTaskRunID))
	if !isFound {
		return task.TaskRun{}, task.ErrTaskRunNotFound
	}
	if sourceTaskRun.Status != task.TaskStatusFailed {
		return task.TaskRun{}, ErrTaskRetryConflict
	}
	sourceEvents := connectorRuntime.taskRunService.ListTaskEvent(sourceTaskRun.TaskRunID)
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(sourceTaskRun, sourceEvents)
	if !isFound {
		return task.TaskRun{}, ErrTaskRetryUnavailable
	}
	if _, errorValue := connectorRuntime.findAdapter(launchContext.Platform); errorValue != nil {
		return task.TaskRun{}, ErrTaskRetryUnavailable
	}
	if connectorRuntime.identityService.ResolvePersonPrimaryEmail(sourceTaskRun.RequesterPersonID) == "" {
		return task.TaskRun{}, ErrTaskRetryUnavailable
	}
	if existingTaskRun, isFound := retryChildForSource(sourceEvents, connectorRuntime.taskRunService); isFound {
		if existingTaskRun.Status == task.TaskStatusPlanned || existingTaskRun.Status == task.TaskStatusInterrupted {
			return existingTaskRun, connectorRuntime.enqueueTaskRetry(sourceTaskRun, existingTaskRun, launchContext)
		}
		return existingTaskRun, nil
	}
	childTaskRun, errorValue := connectorRuntime.taskRunService.CreateTaskRunWithOriginAndError(sourceTaskRun.RequesterPersonID, task.TaskRunOrigin{
		ConversationID: launchContext.ConversationID,
		ReplyTargetID:  launchContext.ReplyTargetID,
		IsThread:       sourceTaskRun.OriginIsThread || launchContext.IsThread,
	}, sourceTaskRun.Prompt)
	if errorValue != nil {
		return task.TaskRun{}, errorValue
	}
	retryRequestBody, errorValue := json.Marshal(TaskRetryReference{TaskRunID: childTaskRun.TaskRunID})
	if errorValue != nil {
		return task.TaskRun{}, errorValue
	}
	if _, errorValue = connectorRuntime.taskRunService.AppendTaskEventWithError(sourceTaskRun.TaskRunID, taskRetryRequestedEvent, string(retryRequestBody)); errorValue != nil {
		_, _ = connectorRuntime.taskRunService.FailTaskRun(childTaskRun.TaskRunID, "could not record retry source: "+errorValue.Error())
		return task.TaskRun{}, errorValue
	}
	return childTaskRun, connectorRuntime.enqueueTaskRetry(sourceTaskRun, childTaskRun, launchContext)
}

func (connectorRuntime *ConnectorRuntime) enqueueTaskRetry(sourceTaskRun task.TaskRun, childTaskRun task.TaskRun, launchContext interruptedTaskLaunchContext) error {
	if !hasTaskRetrySource(connectorRuntime.taskRunService.ListTaskEvent(childTaskRun.TaskRunID), sourceTaskRun.TaskRunID) {
		body := marshalConnectorEventBody(TaskRetryReference{SourceTaskRunID: sourceTaskRun.TaskRunID})
		if _, errorValue := connectorRuntime.taskRunService.AppendTaskEventWithError(childTaskRun.TaskRunID, taskRetrySourceEvent, body); errorValue != nil {
			return errorValue
		}
	}
	_, _, errorValue := connectorRuntime.queueRepository().TryEnqueueConnectorEvent(retryInboundEvent(sourceTaskRun, childTaskRun, launchContext))
	if errorValue != nil {
		connectorRuntime.taskRunService.AppendTaskEvent(childTaskRun.TaskRunID, "task.retry_enqueue_failed", marshalConnectorEventBody(map[string]string{"error": errorValue.Error()}))
	}
	return errorValue
}

func hasTaskRetrySource(events []task.TaskEvent, sourceTaskRunID string) bool {
	for _, event := range events {
		var reference TaskRetryReference
		if event.Name == taskRetrySourceEvent && json.Unmarshal([]byte(event.Body), &reference) == nil && reference.SourceTaskRunID == sourceTaskRunID {
			return true
		}
	}
	return false
}

func retryChildForSource(events []task.TaskEvent, service *task.TaskRunService) (task.TaskRun, bool) {
	for _, event := range events {
		if event.Name != taskRetryRequestedEvent {
			continue
		}
		var reference TaskRetryReference
		if json.Unmarshal([]byte(event.Body), &reference) != nil || reference.TaskRunID == "" {
			continue
		}
		child, isFound := service.FindTaskRun(reference.TaskRunID)
		if isFound {
			return child, true
		}
	}
	return task.TaskRun{}, false
}

func retryInboundEvent(sourceTaskRun task.TaskRun, childTaskRun task.TaskRun, launchContext interruptedTaskLaunchContext) PlatformInboundEvent {
	event := interruptedTaskResumeEvent(childTaskRun, launchContext)
	event.Source = "task_retry"
	event.MessageID = "task_retry:" + childTaskRun.TaskRunID
	event.EventID = event.MessageID
	event.IsThread = &childTaskRun.OriginIsThread
	event.TaskRetry = &TaskRetryReference{SourceTaskRunID: sourceTaskRun.TaskRunID, TaskRunID: childTaskRun.TaskRunID}
	return event
}

func (connectorRuntime *ConnectorRuntime) processTaskRetry(ctx context.Context, adapter PlatformAdapter, event PlatformInboundEvent, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) (ConnectorRuntimeResult, error) {
	if connectorRuntime.taskIntakeGate != nil && connectorRuntime.taskIntakeGate.IsQuiesced() {
		return ConnectorRuntimeResult{Ignored: true, Reason: "task_intake_quiesced"}, nil
	}
	if event.TaskRetry == nil || event.TaskRetry.TaskRunID == "" || event.TaskRetry.SourceTaskRunID == "" {
		return ConnectorRuntimeResult{}, ErrTaskRetryUnavailable
	}
	sourceTaskRun, sourceFound := connectorRuntime.taskRunService.FindTaskRun(event.TaskRetry.SourceTaskRunID)
	childTaskRun, childFound := connectorRuntime.taskRunService.FindTaskRun(event.TaskRetry.TaskRunID)
	if !sourceFound || !childFound || childTaskRun.RequesterPersonID != sourceTaskRun.RequesterPersonID {
		return ConnectorRuntimeResult{}, ErrTaskRetryUnavailable
	}
	if childTaskRun.Status != task.TaskStatusPlanned && childTaskRun.Status != task.TaskStatusInterrupted {
		return ConnectorRuntimeResult{Handled: true, Platform: adapter.Name(), TaskRunID: childTaskRun.TaskRunID, Duplicate: true}, nil
	}
	events := connectorRuntime.taskRunService.ListTaskEvent(sourceTaskRun.TaskRunID)
	linkedChild, isLinked := retryChildForSource(events, connectorRuntime.taskRunService)
	if !isLinked || linkedChild.TaskRunID != childTaskRun.TaskRunID || !hasTaskRetrySource(connectorRuntime.taskRunService.ListTaskEvent(childTaskRun.TaskRunID), sourceTaskRun.TaskRunID) {
		return ConnectorRuntimeResult{}, ErrTaskRetryUnavailable
	}
	launchContext, contextFound := interruptedTaskLaunchContextFromEvents(sourceTaskRun, events)
	if !contextFound {
		return ConnectorRuntimeResult{}, ErrTaskRetryUnavailable
	}
	event = retryInboundEvent(sourceTaskRun, childTaskRun, launchContext)
	ctx = withConnectorEvent(ctx, event)
	request := connectorRuntime.interruptedRetryLaunchRequest(sourceTaskRun, childTaskRun, events, launchContext, event, adapter, sendReply)
	launchResult, errorValue := connectorRuntime.currentTaskLauncher().Launch(ctx, request)
	if errorValue != nil {
		replyTarget := ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID, DedupeKey: event.DedupeKey()}
		turnResult := connectorRuntime.launchFailureCompleter.CompleteLaunchFailure(ctx, agentcontract.AgentTurnRequest{ExistingTaskRunID: childTaskRun.TaskRunID, RequesterPersonID: childTaskRun.RequesterPersonID, Prompt: childTaskRun.Prompt}, "launch", "task_retry", errorValue)
		return connectorRuntime.dispatchTaskReply(ctx, adapter.Name(), adapter, event, replyTarget, turnResult, "", sendReply)
	}
	return connectorRuntime.dispatchTaskReply(ctx, adapter.Name(), adapter, event, ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID, DedupeKey: event.DedupeKey()}, launchResult.TurnResult, "", sendReply)
}

func (connectorRuntime *ConnectorRuntime) interruptedRetryLaunchRequest(sourceTaskRun task.TaskRun, childTaskRun task.TaskRun, events []task.TaskEvent, launchContext interruptedTaskLaunchContext, event PlatformInboundEvent, adapter PlatformAdapter, sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error)) agentruntime.TaskLaunchRequest {
	request := connectorRuntime.interruptedTaskLaunchRequest(sourceTaskRun, events, launchContext, event, adapter, taskResumeProfile{sourceReference: event.DedupeKey()}, sendReply)
	request.ExistingTaskRunID = childTaskRun.TaskRunID
	request.IsTaskRunOpenedForThisTurn = true
	request.IsApprovalContinuation = false
	request.IsRuntimeRestartResume = false
	request.ActiveGoal = agentcontract.ActiveGoal{}
	request.PriorTask = priorTaskContextForTaskRun(sourceTaskRun, events)
	return request
}

func (connectorRuntime *ConnectorRuntime) pendingRetrySource(events []task.TaskEvent) (task.TaskRun, bool) {
	for _, event := range events {
		if event.Name == agentcontract.TaskEventAgentTaskLaunched {
			return task.TaskRun{}, false
		}
	}
	for _, event := range events {
		var reference TaskRetryReference
		if event.Name == taskRetrySourceEvent && json.Unmarshal([]byte(event.Body), &reference) == nil {
			return connectorRuntime.taskRunService.FindTaskRun(reference.SourceTaskRunID)
		}
	}
	return task.TaskRun{}, false
}

func (connectorRuntime *ConnectorRuntime) resumePendingTaskRetry(ctx context.Context, sourceTaskRun task.TaskRun, childTaskRun task.TaskRun) (ConnectorRuntimeResult, error) {
	events := connectorRuntime.taskRunService.ListTaskEvent(sourceTaskRun.TaskRunID)
	launchContext, isFound := interruptedTaskLaunchContextFromEvents(sourceTaskRun, events)
	if !isFound {
		return ConnectorRuntimeResult{}, ErrTaskRetryUnavailable
	}
	adapter, errorValue := connectorRuntime.findAdapter(launchContext.Platform)
	if errorValue != nil {
		return ConnectorRuntimeResult{}, errorValue
	}
	event := retryInboundEvent(sourceTaskRun, childTaskRun, launchContext)
	lock := connectorRuntime.conversationLock(event.Platform + ":" + event.ConversationID)
	lock.Lock()
	defer lock.Unlock()
	return connectorRuntime.processTaskRetry(withConnectorEvent(ctx, event), adapter, event, connectorRuntime.enqueueConnectorReply)
}
