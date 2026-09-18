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
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const scheduleLeaseDuration = 15 * time.Minute
const defaultStaleScheduledTaskRunTimeout = 30 * time.Minute

type ScheduleDeliveryRepository interface {
	EnqueueScheduledConnectorReply(task.Schedule, string, connectors.OutboundReply) (string, error)
}

type ScheduleTransactionalDeliveryRepository interface {
	MarkScheduleSucceededAndEnqueueDelivery(task.Schedule, string, string, connectors.OutboundReply) (string, error)
}

type MorningBriefingOccurrenceRepository interface {
	RetireMorningBriefingOccurrence(task.Schedule, string, time.Time) error
}

type PersonAccessResolver interface {
	ResolvePersonAccess(string) policy.PersonAccess
}

type TaskIntakeGate interface {
	IsQuiesced() bool
}

type SchedulePoller struct {
	ScheduleRepository   task.ScheduleRepository
	DeliveryRepository   ScheduleDeliveryRepository
	ScheduleRunner       agentruntime.ScheduleRunner
	TaskRunService       *task.TaskRunService
	PersonAccessResolver PersonAccessResolver
	TaskIntakeGate       TaskIntakeGate
	WorkspaceID          string
	WorkerID             string
	Logger               *slog.Logger
	StaleTaskRunTimeout  time.Duration
	MorningBriefing      *MorningBriefing
}

type scheduleExecutionResult struct {
	Schedule  task.Schedule
	TaskRunID string
	Reply     connectors.OutboundReply
	DidRun    bool
}

type scheduleTerminalError struct {
	message string
}

func (errorValue scheduleTerminalError) Error() string {
	return errorValue.message
}

func (schedulePoller SchedulePoller) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	schedulePoller.logger().Info("task_schedule.poller.started", "intervalSecond", int(interval.Seconds()), "workerID", schedulePoller.workerID())
	for ctx.Err() == nil {
		if runCount, errorValue := schedulePoller.RunDue(ctx, time.Now().UTC(), 10); errorValue != nil {
			schedulePoller.logger().Error("task_schedule.poller.failed", "error", errorValue.Error())
		} else if runCount > 0 {
			schedulePoller.logger().Info("task_schedule.poller.completed", "runCount", runCount)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (schedulePoller SchedulePoller) RunDue(ctx context.Context, referenceTime time.Time, limit int) (int, error) {
	if schedulePoller.ScheduleRepository == nil {
		return 0, errors.New("task schedule repository is unavailable")
	}
	if schedulePoller.TaskIntakeGate != nil && schedulePoller.TaskIntakeGate.IsQuiesced() {
		return 0, nil
	}
	schedulePoller.cancelStaleScheduledTaskRuns(referenceTime)
	if schedulePoller.MorningBriefing != nil {
		if errorValue := schedulePoller.MorningBriefing.Reconcile(ctx, referenceTime); errorValue != nil {
			schedulePoller.logger().Error("morning_briefing.reconcile_failed", "error", errorValue)
		}
	}
	schedules, errorValue := schedulePoller.ScheduleRepository.ClaimDueSchedules(limit, scheduleLeaseDuration, referenceTime, schedulePoller.workerID())
	if errorValue != nil {
		return 0, errorValue
	}
	runCount := 0
	for _, schedule := range schedules {
		if ctx.Err() != nil {
			return runCount, ctx.Err()
		}
		if schedulePoller.hasActiveScheduleRun(schedule) {
			schedulePoller.logger().Warn("task_schedule.run.skipped_active", "taskScheduleID", schedule.ScheduleID)
			continue
		}
		if errorValue := schedulePoller.runSchedule(ctx, schedule, referenceTime); errorValue != nil {
			schedulePoller.logger().Error(
				"task_schedule.run.failed",
				"taskScheduleID",
				schedule.ScheduleID,
				"nextRunAt",
				schedule.NextRunAt,
				"error",
				errorValue.Error(),
			)
			_ = schedulePoller.recordScheduleFailure(schedule, errorValue, referenceTime)
			continue
		}
		schedulePoller.logger().Info("task_schedule.run.completed", "taskScheduleID", schedule.ScheduleID)
		runCount++
	}
	return runCount, nil
}

const maxScheduleFailureCount = 5

func (schedulePoller SchedulePoller) recordScheduleFailure(schedule task.Schedule, errorValue error, referenceTime time.Time) error {
	if scheduleFailureIsTerminal(schedule, errorValue, referenceTime) {
		if task.IsMorningBriefing(schedule) {
			if repository, isSupported := schedulePoller.ScheduleRepository.(MorningBriefingOccurrenceRepository); isSupported {
				return repository.RetireMorningBriefingOccurrence(schedule, errorValue.Error(), referenceTime)
			}
		}
		return schedulePoller.ScheduleRepository.ExpireSchedule(schedule, errorValue.Error(), referenceTime)
	}
	return schedulePoller.ScheduleRepository.MarkScheduleFailed(schedule, errorValue.Error(), referenceTime)
}

func scheduleFailureIsTerminal(schedule task.Schedule, errorValue error, _ time.Time) bool {
	if schedule.FailureCount+1 >= maxScheduleFailureCount {
		return true
	}
	var terminalError scheduleTerminalError
	return errors.As(errorValue, &terminalError)
}

func (schedulePoller SchedulePoller) runSchedule(ctx context.Context, schedule task.Schedule, referenceTime time.Time) error {
	if canRun, errorValue := schedulePoller.canRunMorningBriefing(ctx, schedule); errorValue != nil || !canRun {
		return schedulePoller.pauseMorningBriefing(schedule, errorValue)
	}
	if errorValue := validateScheduleDeliveryTarget(schedule); errorValue != nil {
		return errorValue
	}
	result, errorValue := schedulePoller.executeSchedule(ctx, schedule, referenceTime)
	if errorValue != nil {
		return errorValue
	}
	if !result.DidRun {
		return schedulePoller.ScheduleRepository.MarkScheduleSucceeded(result.Schedule)
	}
	if canRun, errorValue := schedulePoller.canRunMorningBriefing(ctx, schedule); errorValue != nil || !canRun {
		return schedulePoller.pauseMorningBriefing(result.Schedule, errorValue)
	}
	if errorValue := schedulePoller.markScheduleSucceededAndEnqueueReply(schedule, result); errorValue != nil {
		schedulePoller.logger().Error(
			"task_schedule.reply.enqueue_failed",
			"taskScheduleID",
			result.Schedule.ScheduleID,
			"taskRunID",
			result.TaskRunID,
			"error",
			errorValue.Error(),
		)
		return errorValue
	}
	return nil
}

func (schedulePoller SchedulePoller) pauseMorningBriefing(schedule task.Schedule, errorValue error) error {
	if errorValue != nil {
		return errorValue
	}
	schedule.NextRunAt = nil
	return schedulePoller.ScheduleRepository.MarkScheduleSucceeded(schedule)
}

func (schedulePoller SchedulePoller) canRunMorningBriefing(ctx context.Context, schedule task.Schedule) (bool, error) {
	if schedulePoller.MorningBriefing == nil {
		return true, nil
	}
	return schedulePoller.MorningBriefing.CanRun(ctx, schedule)
}

func (schedulePoller SchedulePoller) executeSchedule(ctx context.Context, schedule task.Schedule, referenceTime time.Time) (scheduleExecutionResult, error) {
	if schedule.ExecutionMode == task.ScheduleExecutionModeMessage {
		return schedulePoller.executeMessageSchedule(schedule, referenceTime)
	}
	return schedulePoller.executeAgentSchedule(ctx, schedule, referenceTime)
}

func (schedulePoller SchedulePoller) executeAgentSchedule(ctx context.Context, schedule task.Schedule, referenceTime time.Time) (scheduleExecutionResult, error) {
	personAccess := policy.PersonAccess{PersonID: schedule.CreatorPersonID}
	if schedulePoller.PersonAccessResolver != nil {
		personAccess = schedulePoller.PersonAccessResolver.ResolvePersonAccess(schedule.CreatorPersonID)
	}
	result, errorValue := schedulePoller.ScheduleRunner.RunIfDue(ctx, agentruntime.ScheduleRunRequest{
		Schedule:      schedule,
		ReferenceTime: referenceTime,
		PersonAccess:  personAccess,
		WorkspaceID:   schedulePoller.WorkspaceID,
	})
	if errorValue != nil {
		return scheduleExecutionResult{}, errorValue
	}
	if !result.DidRun {
		return scheduleExecutionResult{Schedule: result.Schedule}, nil
	}
	reply, errorValue := scheduledTaskReply(result)
	if errorValue != nil {
		var terminalError scheduleTerminalError
		if errors.As(errorValue, &terminalError) {
			terminalErrorMessage := errorValue.Error()
			if schedulePoller.TaskRunService == nil {
				return scheduleExecutionResult{}, errors.New("task run service is unavailable while recording scheduled interaction")
			}
			if _, failError := schedulePoller.TaskRunService.FailTaskRun(result.LaunchResult.TurnResult.TaskRun.TaskRunID, terminalErrorMessage); failError != nil {
				return scheduleExecutionResult{}, failError
			}
		}
		return scheduleExecutionResult{}, errorValue
	}
	return scheduleExecutionResult{
		Schedule:  result.Schedule,
		TaskRunID: result.LaunchResult.TurnResult.TaskRun.TaskRunID,
		Reply:     reply,
		DidRun:    true,
	}, nil
}

func (schedulePoller SchedulePoller) executeMessageSchedule(schedule task.Schedule, referenceTime time.Time) (scheduleExecutionResult, error) {
	if !(task.Scheduler{}).IsScheduleDue(schedule, referenceTime) {
		return scheduleExecutionResult{Schedule: schedule}, nil
	}
	if schedulePoller.TaskRunService == nil {
		return scheduleExecutionResult{}, errors.New("task run service is unavailable")
	}
	taskRun := schedulePoller.TaskRunService.CreateTaskRun(schedule.CreatorPersonID, "schedule:"+schedule.ScheduleID, schedule.Prompt)
	if _, errorValue := schedulePoller.TaskRunService.AdvanceTaskRun(taskRun.TaskRunID, firstNonEmptyString(schedule.AgentProfileName, "default")); errorValue != nil {
		return scheduleExecutionResult{}, errorValue
	}
	completedTaskRun, errorValue := schedulePoller.TaskRunService.CompleteTaskRun(taskRun.TaskRunID, schedule.Prompt)
	if errorValue != nil {
		return scheduleExecutionResult{}, errorValue
	}
	advancedSchedule, errorValue := (task.Scheduler{}).AdvanceSchedule(schedule, referenceTime)
	if errorValue != nil {
		return scheduleExecutionResult{}, errorValue
	}
	advancedSchedule.LastTaskRunID = completedTaskRun.TaskRunID
	return scheduleExecutionResult{
		Schedule:  advancedSchedule,
		TaskRunID: completedTaskRun.TaskRunID,
		Reply:     connectors.OutboundReply{Message: schedule.Prompt, TaskRunID: completedTaskRun.TaskRunID, ReplyKind: "success"},
		DidRun:    true,
	}, nil
}

func (schedulePoller SchedulePoller) markScheduleSucceededAndEnqueueReply(claimedSchedule task.Schedule, result scheduleExecutionResult) error {
	reply := normalizedScheduleReply(result)
	deliveryDeduplicationKey := scheduleDeliveryDeduplicationKey(claimedSchedule)
	if transactionalRepository, ok := schedulePoller.ScheduleRepository.(ScheduleTransactionalDeliveryRepository); ok {
		outboxID, errorValue := transactionalRepository.MarkScheduleSucceededAndEnqueueDelivery(result.Schedule, result.TaskRunID, deliveryDeduplicationKey, reply)
		schedulePoller.recordScheduleDeliveryEvent(result, reply, outboxID, errorValue)
		if errorValue != nil {
			return errorValue
		}
		schedulePoller.logScheduleReplyEnqueued(result, outboxID)
		return nil
	}
	outboxID, errorValue := schedulePoller.enqueuePreparedScheduleReply(result, reply)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := schedulePoller.ScheduleRepository.MarkScheduleSucceeded(result.Schedule); errorValue != nil {
		return errorValue
	}
	schedulePoller.logScheduleReplyEnqueued(result, outboxID)
	return nil
}

func (schedulePoller SchedulePoller) enqueuePreparedScheduleReply(result scheduleExecutionResult, reply connectors.OutboundReply) (string, error) {
	if schedulePoller.DeliveryRepository == nil {
		return "", errors.New("task schedule delivery repository is unavailable")
	}
	outboxID, errorValue := schedulePoller.DeliveryRepository.EnqueueScheduledConnectorReply(result.Schedule, result.TaskRunID, reply)
	schedulePoller.recordScheduleDeliveryEvent(result, reply, outboxID, errorValue)
	return outboxID, errorValue
}

func (schedulePoller SchedulePoller) recordScheduleDeliveryEvent(result scheduleExecutionResult, reply connectors.OutboundReply, outboxID string, errorValue error) {
	if schedulePoller.TaskRunService != nil && strings.TrimSpace(result.TaskRunID) != "" {
		eventName := agentcontract.TaskEventTaskScheduleDeliveryEnqueued
		eventBody := map[string]string{
			"taskScheduleID": result.Schedule.ScheduleID,
			"taskRunID":      result.TaskRunID,
			"replyKind":      reply.ReplyKind,
			"outboxID":       outboxID,
		}
		if errorValue != nil {
			eventName = agentcontract.TaskEventTaskScheduleDeliveryFailed
			eventBody["error"] = errorValue.Error()
		}
		schedulePoller.TaskRunService.AppendTaskEvent(result.TaskRunID, eventName, marshalScheduleEventBody(eventBody))
	}
}

func (schedulePoller SchedulePoller) logScheduleReplyEnqueued(result scheduleExecutionResult, outboxID string) {
	schedulePoller.logger().Info(
		"task_schedule.reply.enqueued",
		"taskScheduleID",
		result.Schedule.ScheduleID,
		"taskRunID",
		result.TaskRunID,
		"outboxID",
		outboxID,
	)
}

func normalizedScheduleReply(result scheduleExecutionResult) connectors.OutboundReply {
	reply := result.Reply
	reply.TaskRunID = firstNonEmptyString(reply.TaskRunID, result.TaskRunID)
	reply.ReplyKind = firstNonEmptyString(reply.ReplyKind, "success")
	return reply
}

func scheduleDeliveryDeduplicationKey(schedule task.Schedule) string {
	occurrenceTime := time.Time{}
	if schedule.NextRunAt != nil {
		occurrenceTime = schedule.NextRunAt.UTC()
	}
	return "schedule:" + strings.TrimSpace(schedule.ScheduleID) + ":occurrence:" + occurrenceTime.Format(time.RFC3339Nano)
}

func scheduledTaskReply(result agentruntime.ScheduleRunResult) (connectors.OutboundReply, error) {
	turnResult := result.LaunchResult.TurnResult
	reply := strings.TrimSpace(turnResult.FinishMessage)
	if turnResult.TaskRun.Status != task.TaskStatusCompleted {
		reason := strings.TrimSpace(turnResult.TaskRun.FailureReason)
		if reason != "" {
			reason = " reason=" + reason
		}
		message := "scheduled task did not complete: taskRunID=" + turnResult.TaskRun.TaskRunID + " status=" + string(turnResult.TaskRun.Status) + reason
		if taskStatusRequiresInteraction(turnResult.TaskRun.Status) {
			return connectors.OutboundReply{}, scheduleTerminalError{message: message}
		}
		return connectors.OutboundReply{}, errors.New(message)
	}
	if reply == "" {
		return connectors.OutboundReply{}, errors.New("scheduled task completed without a reply")
	}
	return connectors.OutboundReply{Message: reply, TaskRunID: turnResult.TaskRun.TaskRunID, ReplyKind: "success", Attachments: turnResult.Attachments}, nil
}

func (schedulePoller SchedulePoller) hasActiveScheduleRun(schedule task.Schedule) bool {
	if schedulePoller.TaskRunService == nil {
		return false
	}
	originConversationID := "schedule:" + strings.TrimSpace(schedule.ScheduleID)
	for _, taskRun := range schedulePoller.TaskRunService.ListTaskRun() {
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

func (schedulePoller SchedulePoller) cancelStaleScheduledTaskRuns(referenceTime time.Time) {
	if schedulePoller.TaskRunService == nil {
		return
	}
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	staleBefore := referenceTime.Add(-schedulePoller.staleTaskRunTimeout())
	cancelledTaskRuns := schedulePoller.TaskRunService.CancelActiveTaskRuns(task.TaskRunCancelRequest{
		OriginConversationIDPrefix: "schedule:",
		ScheduleOnly:               true,
		StaleBefore:                &staleBefore,
		Reason:                     "scheduled task stale timeout",
	})
	if len(cancelledTaskRuns) > 0 {
		schedulePoller.logger().Warn("task_schedule.stale_runs_cancelled", "count", len(cancelledTaskRuns))
	}
}

func (schedulePoller SchedulePoller) staleTaskRunTimeout() time.Duration {
	if schedulePoller.StaleTaskRunTimeout > 0 {
		return schedulePoller.StaleTaskRunTimeout
	}
	return defaultStaleScheduledTaskRunTimeout
}

func validateScheduleDeliveryTarget(schedule task.Schedule) error {
	if strings.TrimSpace(schedule.Platform) == "" {
		return scheduleTerminalError{message: "scheduled task platform is required"}
	}
	if strings.TrimSpace(schedule.ConversationID) == "" {
		return scheduleTerminalError{message: "scheduled task conversation is required"}
	}
	if strings.TrimSpace(schedule.ReplyTargetID) == "" {
		return scheduleTerminalError{message: "scheduled task reply target is required"}
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

func (schedulePoller SchedulePoller) workerID() string {
	if strings.TrimSpace(schedulePoller.WorkerID) != "" {
		return strings.TrimSpace(schedulePoller.WorkerID)
	}
	return "blueclaw-task-schedule-poller"
}

func (schedulePoller SchedulePoller) logger() *slog.Logger {
	if schedulePoller.Logger != nil {
		return schedulePoller.Logger
	}
	return slog.Default()
}

func taskStatusRequiresInteraction(status task.TaskStatus) bool {
	return status == task.TaskStatusWaitingApproval || status == task.TaskStatusWaitingUserInput || status == task.TaskStatusBlocked
}
