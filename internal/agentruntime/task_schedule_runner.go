package agentruntime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TaskScheduleRunner struct {
	taskLauncher  *TaskLauncher
	taskScheduler task.TaskScheduler
	workspaceID   string
}

type TaskScheduleRunRequest struct {
	TaskSchedule  task.TaskSchedule
	ReferenceTime time.Time
	PersonAccess  policy.PersonAccess
	WorkspaceID   string
}

type TaskScheduleRunResult struct {
	TaskSchedule task.TaskSchedule
	LaunchResult TaskLaunchResult
	DidRun       bool
}

func NewTaskScheduleRunner(taskLauncher *TaskLauncher) TaskScheduleRunner {
	return TaskScheduleRunner{
		taskLauncher:  taskLauncher,
		taskScheduler: task.TaskScheduler{},
	}
}

func (taskScheduleRunner TaskScheduleRunner) RunIfDue(ctx context.Context, request TaskScheduleRunRequest) (TaskScheduleRunResult, error) {
	referenceTime := request.ReferenceTime
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	taskSchedule := request.TaskSchedule
	if !taskScheduleRunner.taskScheduler.IsTaskScheduleDue(taskSchedule, referenceTime) {
		return TaskScheduleRunResult{TaskSchedule: taskSchedule}, nil
	}
	workspaceID := request.WorkspaceID
	if workspaceID == "" {
		workspaceID = taskScheduleRunner.workspaceID
	}
	launchResult, errorValue := taskScheduleRunner.taskLauncher.Launch(ctx, TaskLaunchRequest{
		Source:                    TaskLaunchSourceScheduled,
		SourceReference:           taskSchedule.TaskScheduleID,
		RequesterPersonID:         taskSchedule.CreatorPersonID,
		IsApprovalContinuation:    true,
		ProfileName:               taskSchedule.AgentProfileName,
		Platform:                  taskSchedule.Platform,
		ConversationID:            "schedule:" + taskSchedule.TaskScheduleID,
		ReplyTargetID:             taskSchedule.ReplyTargetID,
		Prompt:                    taskSchedule.Prompt,
		ScheduledRun:              scheduledRunContext(taskSchedule, referenceTime),
		PersonAccess:              request.PersonAccess,
		MemoryLabel:               memory.SecurityLabel{SecurityLevelRank: request.PersonAccess.SecurityLevelRank, RequiredClasses: append([]string{}, request.PersonAccess.GrantedClasses...)},
		AccessibleConversationIDs: []string{"schedule:" + taskSchedule.TaskScheduleID},
	})
	if errorValue != nil {
		return TaskScheduleRunResult{}, errorValue
	}
	advancedTaskSchedule, errorValue := taskScheduleRunner.taskScheduler.AdvanceTaskSchedule(taskSchedule, referenceTime)
	if errorValue != nil {
		return TaskScheduleRunResult{}, errorValue
	}
	advancedTaskSchedule.LastTaskRunID = launchResult.TurnResult.TaskRun.TaskRunID
	return TaskScheduleRunResult{TaskSchedule: advancedTaskSchedule, LaunchResult: launchResult, DidRun: true}, nil
}

func scheduledRunContext(taskSchedule task.TaskSchedule, referenceTime time.Time) agentcontract.ScheduledRunContext {
	timeZone := taskScheduleTimeZone(taskSchedule)
	return agentcontract.ScheduledRunContext{
		ScheduleID:        strings.TrimSpace(taskSchedule.TaskScheduleID),
		Name:              strings.TrimSpace(taskSchedule.Name),
		Kind:              string(taskSchedule.Kind),
		Cadence:           describeTaskScheduleCadence(taskSchedule),
		CronExpression:    strings.TrimSpace(taskSchedule.CronExpression),
		TimeZone:          timeZone,
		OccurrenceAt:      formatScheduledRunTime(scheduledRunOccurrenceAt(taskSchedule, referenceTime), timeZone),
		RunAt:             formatOptionalScheduledRunTime(taskSchedule.RunAt, timeZone),
		IntervalSecond:    taskSchedule.IntervalSecond,
		CompletedRunCount: taskSchedule.CompletedRunCount,
		MaxRunCount:       taskSchedule.MaxRunCount,
		LastRunAt:         formatOptionalScheduledRunTime(taskSchedule.LastRunAt, timeZone),
		NextRunAt:         formatOptionalScheduledRunTime(taskSchedule.NextRunAt, timeZone),
		ExpiresAt:         formatOptionalScheduledRunTime(taskSchedule.ExpiresAt, timeZone),
	}
}

func scheduledRunOccurrenceAt(taskSchedule task.TaskSchedule, referenceTime time.Time) time.Time {
	if taskSchedule.NextRunAt != nil && !taskSchedule.NextRunAt.IsZero() {
		return *taskSchedule.NextRunAt
	}
	return referenceTime
}

func describeTaskScheduleCadence(taskSchedule task.TaskSchedule) string {
	switch taskSchedule.Kind {
	case task.TaskScheduleKindOnce:
		if taskSchedule.RunAt == nil {
			return "one-time"
		}
		return "one-time at " + formatScheduledRunTime(*taskSchedule.RunAt, taskScheduleTimeZone(taskSchedule))
	case task.TaskScheduleKindInterval:
		if taskSchedule.IntervalSecond <= 0 {
			return "interval"
		}
		return "every " + (time.Duration(taskSchedule.IntervalSecond) * time.Second).String()
	case task.TaskScheduleKindCron:
		return describeCronScheduleCadence(taskSchedule)
	default:
		return string(taskSchedule.Kind)
	}
}

func describeCronScheduleCadence(taskSchedule task.TaskSchedule) string {
	cronExpression := strings.TrimSpace(taskSchedule.CronExpression)
	timeZone := taskScheduleTimeZone(taskSchedule)
	fieldTexts := strings.Fields(cronExpression)
	if len(fieldTexts) != 5 {
		return strings.TrimSpace("cron " + cronExpression + " " + timeZone)
	}
	timeText := singleCronTimeText(fieldTexts[1], fieldTexts[0])
	if timeText == "" {
		return "cron " + cronExpression + " " + timeZone
	}
	if fieldTexts[2] == "*" && fieldTexts[3] == "*" && fieldTexts[4] == "*" {
		return "daily at " + timeText + " " + timeZone
	}
	return "cron " + cronExpression + " at " + timeText + " " + timeZone
}

func singleCronTimeText(hourText string, minuteText string) string {
	hour, errorValue := strconv.Atoi(hourText)
	if errorValue != nil || hour < 0 || hour > 23 {
		return ""
	}
	minute, errorValue := strconv.Atoi(minuteText)
	if errorValue != nil || minute < 0 || minute > 59 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func taskScheduleTimeZone(taskSchedule task.TaskSchedule) string {
	return task.ScheduleTimeZoneName(taskSchedule.TimeZone)
}

func formatOptionalScheduledRunTime(timeValue *time.Time, timeZone string) string {
	if timeValue == nil {
		return ""
	}
	return formatScheduledRunTime(*timeValue, timeZone)
}

func formatScheduledRunTime(timeValue time.Time, timeZone string) string {
	if timeValue.IsZero() {
		return ""
	}
	location, errorValue := time.LoadLocation(timeZone)
	if errorValue != nil {
		return timeValue.UTC().Format(time.RFC3339)
	}
	return timeValue.In(location).Format(time.RFC3339)
}
