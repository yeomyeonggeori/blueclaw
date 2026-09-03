package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const taskScheduleLeaseDuration = 15 * time.Minute
const defaultStaleScheduledTaskRunTimeout = 30 * time.Minute

type TaskScheduleDeliveryRepository interface {
	EnqueueScheduledConnectorReply(task.TaskSchedule, string, connectors.OutboundReply) (string, error)
}

type TaskScheduleTransactionalDeliveryRepository interface {
	MarkTaskScheduleSucceededAndEnqueueDelivery(task.TaskSchedule, string, string, connectors.OutboundReply) (string, error)
}

type PersonAccessResolver interface {
	ResolvePersonAccess(string) policy.PersonAccess
}

type TaskIntakeGate interface {
	IsQuiesced() bool
}

type TaskSchedulePoller struct {
	TaskScheduleRepository task.TaskScheduleRepository
	DeliveryRepository     TaskScheduleDeliveryRepository
	TaskScheduleRunner     agentruntime.TaskScheduleRunner
	TaskRunService         *task.TaskRunService
	PersonAccessResolver   PersonAccessResolver
	TaskIntakeGate         TaskIntakeGate
	WorkspaceID            string
	WorkerID               string
	Logger                 *slog.Logger
	StaleTaskRunTimeout    time.Duration
}

type taskScheduleExecutionResult struct {
	TaskSchedule task.TaskSchedule
	TaskRunID    string
	Reply        connectors.OutboundReply
	DidRun       bool
}

type taskScheduleTerminalError struct {
	message string
}

func (errorValue taskScheduleTerminalError) Error() string {
	return errorValue.message
}

func (taskSchedulePoller TaskSchedulePoller) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	taskSchedulePoller.logger().Info("task_schedule.poller.started", "intervalSecond", int(interval.Seconds()), "workerID", taskSchedulePoller.workerID())
	for ctx.Err() == nil {
		if runCount, errorValue := taskSchedulePoller.RunDue(ctx, time.Now().UTC(), 10); errorValue != nil {
			taskSchedulePoller.logger().Error("task_schedule.poller.failed", "error", errorValue.Error())
		} else if runCount > 0 {
			taskSchedulePoller.logger().Info("task_schedule.poller.completed", "runCount", runCount)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (taskSchedulePoller TaskSchedulePoller) RunDue(ctx context.Context, referenceTime time.Time, limit int) (int, error) {
	if taskSchedulePoller.TaskScheduleRepository == nil {
		return 0, errors.New("task schedule repository is unavailable")
	}
	if taskSchedulePoller.TaskIntakeGate != nil && taskSchedulePoller.TaskIntakeGate.IsQuiesced() {
		return 0, nil
	}
	taskSchedulePoller.cancelStaleScheduledTaskRuns(referenceTime)
	taskSchedules, errorValue := taskSchedulePoller.TaskScheduleRepository.ClaimDueTaskSchedules(limit, taskScheduleLeaseDuration, referenceTime, taskSchedulePoller.workerID())
	if errorValue != nil {
		return 0, errorValue
	}
	runCount := 0
	for _, taskSchedule := range taskSchedules {
		if ctx.Err() != nil {
			return runCount, ctx.Err()
		}
		if taskSchedulePoller.hasActiveTaskScheduleRun(taskSchedule) {
			taskSchedulePoller.logger().Warn("task_schedule.run.skipped_active", "taskScheduleID", taskSchedule.TaskScheduleID)
			continue
		}
		if errorValue := taskSchedulePoller.runTaskSchedule(ctx, taskSchedule, referenceTime); errorValue != nil {
			taskSchedulePoller.logger().Error(
				"task_schedule.run.failed",
				"taskScheduleID",
				taskSchedule.TaskScheduleID,
				"nextRunAt",
				taskSchedule.NextRunAt,
				"error",
				errorValue.Error(),
			)
			_ = taskSchedulePoller.recordTaskScheduleFailure(taskSchedule, errorValue, referenceTime)
			continue
		}
		taskSchedulePoller.logger().Info("task_schedule.run.completed", "taskScheduleID", taskSchedule.TaskScheduleID)
		runCount++
	}
	return runCount, nil
}

const maxTaskScheduleFailureCount = 5

func (taskSchedulePoller TaskSchedulePoller) recordTaskScheduleFailure(taskSchedule task.TaskSchedule, errorValue error, referenceTime time.Time) error {
	if taskScheduleFailureIsTerminal(taskSchedule, errorValue, referenceTime) {
		return taskSchedulePoller.TaskScheduleRepository.ExpireTaskSchedule(taskSchedule, errorValue.Error(), referenceTime)
	}
	return taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleFailed(taskSchedule, errorValue.Error(), referenceTime)
}

func taskScheduleFailureIsTerminal(taskSchedule task.TaskSchedule, errorValue error, _ time.Time) bool {
	if taskSchedule.FailureCount+1 >= maxTaskScheduleFailureCount {
		return true
	}
	var terminalError taskScheduleTerminalError
	return errors.As(errorValue, &terminalError)
}

func (taskSchedulePoller TaskSchedulePoller) runTaskSchedule(ctx context.Context, taskSchedule task.TaskSchedule, referenceTime time.Time) error {
	if errorValue := validateTaskScheduleDeliveryTarget(taskSchedule); errorValue != nil {
		return errorValue
	}
	result, errorValue := taskSchedulePoller.executeTaskSchedule(ctx, taskSchedule, referenceTime)
	if errorValue != nil {
		return errorValue
	}
	if !result.DidRun {
		return taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleSucceeded(result.TaskSchedule)
	}
	if errorValue := taskSchedulePoller.markTaskScheduleSucceededAndEnqueueReply(taskSchedule, result); errorValue != nil {
		taskSchedulePoller.logger().Error(
			"task_schedule.reply.enqueue_failed",
			"taskScheduleID",
			result.TaskSchedule.TaskScheduleID,
			"taskRunID",
			result.TaskRunID,
			"error",
			errorValue.Error(),
		)
		return errorValue
	}
	return nil
}

func (taskSchedulePoller TaskSchedulePoller) executeTaskSchedule(ctx context.Context, taskSchedule task.TaskSchedule, referenceTime time.Time) (taskScheduleExecutionResult, error) {
	if taskSchedule.ExecutionMode == task.TaskScheduleExecutionModeMessage {
		return taskSchedulePoller.executeMessageTaskSchedule(taskSchedule, referenceTime)
	}
	return taskSchedulePoller.executeAgentTaskSchedule(ctx, taskSchedule, referenceTime)
}

func (taskSchedulePoller TaskSchedulePoller) executeAgentTaskSchedule(ctx context.Context, taskSchedule task.TaskSchedule, referenceTime time.Time) (taskScheduleExecutionResult, error) {
	personAccess := policy.PersonAccess{PersonID: taskSchedule.CreatorPersonID}
	if taskSchedulePoller.PersonAccessResolver != nil {
		personAccess = taskSchedulePoller.PersonAccessResolver.ResolvePersonAccess(taskSchedule.CreatorPersonID)
	}
	result, errorValue := taskSchedulePoller.TaskScheduleRunner.RunIfDue(ctx, agentruntime.TaskScheduleRunRequest{
		TaskSchedule:  taskSchedule,
		ReferenceTime: referenceTime,
		PersonAccess:  personAccess,
		WorkspaceID:   taskSchedulePoller.WorkspaceID,
	})
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	if !result.DidRun {
		return taskScheduleExecutionResult{TaskSchedule: result.TaskSchedule}, nil
	}
	reply, errorValue := scheduledTaskReply(result)
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	return taskScheduleExecutionResult{
		TaskSchedule: result.TaskSchedule,
		TaskRunID:    result.LaunchResult.TurnResult.TaskRun.TaskRunID,
		Reply:        reply,
		DidRun:       true,
	}, nil
}

func (taskSchedulePoller TaskSchedulePoller) executeMessageTaskSchedule(taskSchedule task.TaskSchedule, referenceTime time.Time) (taskScheduleExecutionResult, error) {
	if !(task.TaskScheduler{}).IsTaskScheduleDue(taskSchedule, referenceTime) {
		return taskScheduleExecutionResult{TaskSchedule: taskSchedule}, nil
	}
	if taskSchedulePoller.TaskRunService == nil {
		return taskScheduleExecutionResult{}, errors.New("task run service is unavailable")
	}
	taskRun := taskSchedulePoller.TaskRunService.CreateTaskRun(taskSchedule.CreatorPersonID, "schedule:"+taskSchedule.TaskScheduleID, taskSchedule.Prompt)
	if _, errorValue := taskSchedulePoller.TaskRunService.AdvanceTaskRun(taskRun.TaskRunID, firstNonEmptyString(taskSchedule.AgentProfileName, "default")); errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	completedTaskRun, errorValue := taskSchedulePoller.TaskRunService.CompleteTaskRun(taskRun.TaskRunID, taskSchedule.Prompt)
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	advancedTaskSchedule, errorValue := (task.TaskScheduler{}).AdvanceTaskSchedule(taskSchedule, referenceTime)
	if errorValue != nil {
		return taskScheduleExecutionResult{}, errorValue
	}
	advancedTaskSchedule.LastTaskRunID = completedTaskRun.TaskRunID
	return taskScheduleExecutionResult{
		TaskSchedule: advancedTaskSchedule,
		TaskRunID:    completedTaskRun.TaskRunID,
		Reply:        connectors.OutboundReply{Message: taskSchedule.Prompt, TaskRunID: completedTaskRun.TaskRunID, ReplyKind: "success"},
		DidRun:       true,
	}, nil
}

func (taskSchedulePoller TaskSchedulePoller) markTaskScheduleSucceededAndEnqueueReply(claimedTaskSchedule task.TaskSchedule, result taskScheduleExecutionResult) error {
	reply := normalizedTaskScheduleReply(result)
	deliveryDeduplicationKey := taskScheduleDeliveryDeduplicationKey(claimedTaskSchedule)
	if transactionalRepository, ok := taskSchedulePoller.TaskScheduleRepository.(TaskScheduleTransactionalDeliveryRepository); ok {
		outboxID, errorValue := transactionalRepository.MarkTaskScheduleSucceededAndEnqueueDelivery(result.TaskSchedule, result.TaskRunID, deliveryDeduplicationKey, reply)
		taskSchedulePoller.recordTaskScheduleDeliveryEvent(result, reply, outboxID, errorValue)
		if errorValue != nil {
			return errorValue
		}
		taskSchedulePoller.logTaskScheduleReplyEnqueued(result, outboxID)
		return nil
	}
	outboxID, errorValue := taskSchedulePoller.enqueuePreparedTaskScheduleReply(result, reply)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := taskSchedulePoller.TaskScheduleRepository.MarkTaskScheduleSucceeded(result.TaskSchedule); errorValue != nil {
		return errorValue
	}
	taskSchedulePoller.logTaskScheduleReplyEnqueued(result, outboxID)
	return nil
}

func (taskSchedulePoller TaskSchedulePoller) enqueuePreparedTaskScheduleReply(result taskScheduleExecutionResult, reply connectors.OutboundReply) (string, error) {
	if taskSchedulePoller.DeliveryRepository == nil {
		return "", errors.New("task schedule delivery repository is unavailable")
	}
	outboxID, errorValue := taskSchedulePoller.DeliveryRepository.EnqueueScheduledConnectorReply(result.TaskSchedule, result.TaskRunID, reply)
	taskSchedulePoller.recordTaskScheduleDeliveryEvent(result, reply, outboxID, errorValue)
	return outboxID, errorValue
}

func (taskSchedulePoller TaskSchedulePoller) recordTaskScheduleDeliveryEvent(result taskScheduleExecutionResult, reply connectors.OutboundReply, outboxID string, errorValue error) {
	if taskSchedulePoller.TaskRunService != nil && strings.TrimSpace(result.TaskRunID) != "" {
		eventName := taskstate.TaskEventTaskScheduleDeliveryEnqueued
		eventBody := map[string]string{
			"taskScheduleID": result.TaskSchedule.TaskScheduleID,
			"taskRunID":      result.TaskRunID,
			"replyKind":      reply.ReplyKind,
			"outboxID":       outboxID,
		}
		if errorValue != nil {
			eventName = taskstate.TaskEventTaskScheduleDeliveryFailed
			eventBody["error"] = errorValue.Error()
		}
		taskSchedulePoller.TaskRunService.AppendTaskEvent(result.TaskRunID, eventName, marshalScheduleEventBody(eventBody))
	}
}

func (taskSchedulePoller TaskSchedulePoller) logTaskScheduleReplyEnqueued(result taskScheduleExecutionResult, outboxID string) {
	taskSchedulePoller.logger().Info(
		"task_schedule.reply.enqueued",
		"taskScheduleID",
		result.TaskSchedule.TaskScheduleID,
		"taskRunID",
		result.TaskRunID,
		"outboxID",
		outboxID,
	)
}

func normalizedTaskScheduleReply(result taskScheduleExecutionResult) connectors.OutboundReply {
	reply := result.Reply
	reply.TaskRunID = firstNonEmptyString(reply.TaskRunID, result.TaskRunID)
	reply.ReplyKind = firstNonEmptyString(reply.ReplyKind, "success")
	return reply
}

func taskScheduleDeliveryDeduplicationKey(taskSchedule task.TaskSchedule) string {
	occurrenceTime := time.Time{}
	if taskSchedule.NextRunAt != nil {
		occurrenceTime = taskSchedule.NextRunAt.UTC()
	}
	return "schedule:" + strings.TrimSpace(taskSchedule.TaskScheduleID) + ":occurrence:" + occurrenceTime.Format(time.RFC3339Nano)
}

func scheduledTaskReply(result agentruntime.TaskScheduleRunResult) (connectors.OutboundReply, error) {
	turnResult := result.LaunchResult.TurnResult
	reply := strings.TrimSpace(turnResult.FinishMessage)
	if turnResult.TaskRun.Status != task.TaskStatusCompleted {
		reason := strings.TrimSpace(turnResult.TaskRun.FailureReason)
		if reason != "" {
			reason = " reason=" + reason
		}
		message := "scheduled task did not complete: taskRunID=" + turnResult.TaskRun.TaskRunID + " status=" + string(turnResult.TaskRun.Status) + reason
		if taskStatusRequiresInteraction(turnResult.TaskRun.Status) {
			return connectors.OutboundReply{}, taskScheduleTerminalError{message: message}
		}
		return connectors.OutboundReply{}, errors.New(message)
	}
	if reply == "" {
		return connectors.OutboundReply{}, errors.New("scheduled task completed without a reply")
	}
	return connectors.OutboundReply{Message: reply, TaskRunID: turnResult.TaskRun.TaskRunID, ReplyKind: "success", Attachments: turnResult.Attachments}, nil
}

func (taskSchedulePoller TaskSchedulePoller) hasActiveTaskScheduleRun(taskSchedule task.TaskSchedule) bool {
	if taskSchedulePoller.TaskRunService == nil {
		return false
	}
	originConversationID := "schedule:" + strings.TrimSpace(taskSchedule.TaskScheduleID)
	for _, taskRun := range taskSchedulePoller.TaskRunService.ListTaskRun() {
		if taskRun.OriginConversationID != originConversationID {
			continue
		}
		switch taskRun.Status {
		case task.TaskStatusPlanned, task.TaskStatusRunning, task.TaskStatusWaitingApproval, task.TaskStatusWaitingUserInput, task.TaskStatusBlocked:
			return true
		}
	}
	return false
}

func marshalScheduleEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}

func (taskSchedulePoller TaskSchedulePoller) cancelStaleScheduledTaskRuns(referenceTime time.Time) {
	if taskSchedulePoller.TaskRunService == nil {
		return
	}
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	staleBefore := referenceTime.Add(-taskSchedulePoller.staleTaskRunTimeout())
	cancelledTaskRuns := taskSchedulePoller.TaskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
		OriginConversationIDPrefix: "schedule:",
		ScheduleOnly:               true,
		StaleBefore:                &staleBefore,
		Reason:                     "scheduled task stale timeout",
	})
	if len(cancelledTaskRuns) > 0 {
		taskSchedulePoller.logger().Warn("task_schedule.stale_runs_cancelled", "count", len(cancelledTaskRuns))
	}
}

func (taskSchedulePoller TaskSchedulePoller) staleTaskRunTimeout() time.Duration {
	if taskSchedulePoller.StaleTaskRunTimeout > 0 {
		return taskSchedulePoller.StaleTaskRunTimeout
	}
	return defaultStaleScheduledTaskRunTimeout
}

func validateTaskScheduleDeliveryTarget(taskSchedule task.TaskSchedule) error {
	if strings.TrimSpace(taskSchedule.Platform) == "" {
		return taskScheduleTerminalError{message: "scheduled task platform is required"}
	}
	if strings.TrimSpace(taskSchedule.ConversationID) == "" {
		return taskScheduleTerminalError{message: "scheduled task conversation is required"}
	}
	if strings.TrimSpace(taskSchedule.ReplyTargetID) == "" {
		return taskScheduleTerminalError{message: "scheduled task reply target is required"}
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (taskSchedulePoller TaskSchedulePoller) workerID() string {
	if strings.TrimSpace(taskSchedulePoller.WorkerID) != "" {
		return strings.TrimSpace(taskSchedulePoller.WorkerID)
	}
	return "blueclaw-task-schedule-poller"
}

func (taskSchedulePoller TaskSchedulePoller) logger() *slog.Logger {
	if taskSchedulePoller.Logger != nil {
		return taskSchedulePoller.Logger
	}
	return slog.Default()
}

func taskStatusRequiresInteraction(status task.TaskStatus) bool {
	return status == task.TaskStatusWaitingApproval || status == task.TaskStatusWaitingUserInput
}
