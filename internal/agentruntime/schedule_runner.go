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

type ScheduleRunner struct {
	taskLauncher *TaskLauncher
	scheduler    task.Scheduler
	workspaceID  string
}

type ScheduleRunRequest struct {
	Schedule         task.Schedule
	ReferenceTime    time.Time
	PersonAccess     policy.PersonAccess
	WorkspaceID      string
	ResponseLanguage string
}

type ScheduleRunResult struct {
	Schedule     task.Schedule
	LaunchResult TaskLaunchResult
	DidRun       bool
}

func NewScheduleRunner(taskLauncher *TaskLauncher) ScheduleRunner {
	return ScheduleRunner{
		taskLauncher: taskLauncher,
		scheduler:    task.Scheduler{},
	}
}

func (scheduleRunner ScheduleRunner) RunIfDue(ctx context.Context, request ScheduleRunRequest) (ScheduleRunResult, error) {
	referenceTime := request.ReferenceTime
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	schedule := request.Schedule
	if !scheduleRunner.scheduler.IsScheduleDue(schedule, referenceTime) {
		return ScheduleRunResult{Schedule: schedule}, nil
	}
	if scheduleRunner.skipEmptyMorningBriefing(ctx, request, referenceTime) {
		advancedSchedule, errorValue := scheduleRunner.scheduler.AdvanceSchedule(schedule, referenceTime)
		return ScheduleRunResult{Schedule: advancedSchedule}, errorValue
	}
	workspaceID := request.WorkspaceID
	if workspaceID == "" {
		workspaceID = scheduleRunner.workspaceID
	}
	responseLanguage := request.ResponseLanguage
	if strings.TrimSpace(responseLanguage) == "" {
		responseLanguage = requesterPersonaLanguage(scheduleRunner.taskLauncher.toolCatalogBuilder.workspaceActorFactory, request.PersonAccess, scheduleRunner.taskLauncher.toolCatalogBuilder.WorkspaceRootPath())
	}
	launchResult, errorValue := scheduleRunner.taskLauncher.Launch(ctx, TaskLaunchRequest{
		Source:                    TaskLaunchSourceScheduled,
		SourceReference:           schedule.ScheduleID,
		RequesterPersonID:         schedule.CreatorPersonID,
		IsApprovalContinuation:    true,
		ProfileName:               schedule.AgentProfileName,
		Platform:                  schedule.Platform,
		ConversationID:            task.ScheduleSessionID(schedule.ScheduleID),
		DeliveryConversationID:    schedule.ConversationID,
		ReplyTargetID:             schedule.ReplyTargetID,
		Prompt:                    schedule.Prompt,
		ResponseLanguage:          responseLanguage,
		ScheduledRun:              scheduledRunContext(schedule, referenceTime),
		PersonAccess:              request.PersonAccess,
		MemoryLabel:               memory.LabelForAccess(request.PersonAccess),
		AccessibleConversationIDs: []string{task.ScheduleSessionID(schedule.ScheduleID)},
	})
	if errorValue != nil {
		return ScheduleRunResult{}, errorValue
	}
	advancedSchedule, errorValue := scheduleRunner.scheduler.AdvanceSchedule(schedule, referenceTime)
	if errorValue != nil {
		return ScheduleRunResult{}, errorValue
	}
	advancedSchedule.LastTaskRunID = launchResult.TurnResult.TaskRun.TaskRunID
	return ScheduleRunResult{Schedule: advancedSchedule, LaunchResult: launchResult, DidRun: true}, nil
}

func scheduledRunContext(schedule task.Schedule, referenceTime time.Time) agentcontract.ScheduledRunContext {
	timeZone := scheduleTimeZone(schedule)
	return agentcontract.ScheduledRunContext{
		ScheduleID:        strings.TrimSpace(schedule.ScheduleID),
		Name:              strings.TrimSpace(schedule.Name),
		Kind:              string(schedule.Kind),
		Cadence:           describeScheduleCadence(schedule),
		CronExpression:    strings.TrimSpace(schedule.CronExpression),
		TimeZone:          timeZone,
		OccurrenceAt:      formatScheduledRunTime(scheduledRunOccurrenceAt(schedule, referenceTime), timeZone),
		RunAt:             formatOptionalScheduledRunTime(schedule.RunAt, timeZone),
		IntervalSecond:    schedule.IntervalSecond,
		CompletedRunCount: schedule.CompletedRunCount,
		MaxRunCount:       schedule.MaxRunCount,
		LastRunAt:         formatOptionalScheduledRunTime(schedule.LastRunAt, timeZone),
		NextRunAt:         formatOptionalScheduledRunTime(schedule.NextRunAt, timeZone),
		ExpiresAt:         formatOptionalScheduledRunTime(schedule.ExpiresAt, timeZone),
	}
}

func scheduledRunOccurrenceAt(schedule task.Schedule, referenceTime time.Time) time.Time {
	if schedule.NextRunAt != nil && !schedule.NextRunAt.IsZero() {
		return *schedule.NextRunAt
	}
	return referenceTime
}

func describeScheduleCadence(schedule task.Schedule) string {
	switch schedule.Kind {
	case task.ScheduleKindOnce:
		if schedule.RunAt == nil {
			return "one-time"
		}
		return "one-time at " + formatScheduledRunTime(*schedule.RunAt, scheduleTimeZone(schedule))
	case task.ScheduleKindInterval:
		if schedule.IntervalSecond <= 0 {
			return "interval"
		}
		return "every " + (time.Duration(schedule.IntervalSecond) * time.Second).String()
	case task.ScheduleKindCron:
		return describeCronScheduleCadence(schedule)
	default:
		return string(schedule.Kind)
	}
}

func describeCronScheduleCadence(schedule task.Schedule) string {
	cronExpression := strings.TrimSpace(schedule.CronExpression)
	timeZone := scheduleTimeZone(schedule)
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

func scheduleTimeZone(schedule task.Schedule) string {
	return task.ScheduleTimeZoneName(schedule.TimeZone)
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
