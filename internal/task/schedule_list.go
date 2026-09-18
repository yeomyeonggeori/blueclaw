package task

import (
	"strconv"
	"strings"
	"time"
)

type ScheduleListInput struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

type ScheduleListOutput struct {
	Schedules []ScheduleListItem `json:"schedules"`
}

type ScheduleListItem struct {
	ScheduleID      string     `json:"scheduleID"`
	TaskInstruction string     `json:"taskInstruction"`
	Description     string     `json:"description,omitempty"`
	Cadence         string     `json:"cadence"`
	CronExpression  string     `json:"cronExpression,omitempty"`
	RunAt           *time.Time `json:"runAt,omitempty"`
	Status          string     `json:"status"`
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt       *time.Time `json:"lastRunAt,omitempty"`
}

func ProjectScheduleList(taskSchedules []TaskSchedule, input ScheduleListInput, referenceTime time.Time) ScheduleListOutput {
	limit := normalizedScheduleListLimit(input.Limit)
	status := strings.TrimSpace(input.Status)
	items := []ScheduleListItem{}
	for _, taskSchedule := range taskSchedules {
		item := scheduleListItemFromSchedule(taskSchedule, referenceTime)
		if status != "" && item.Status != status {
			continue
		}
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	return ScheduleListOutput{Schedules: items}
}

func normalizedScheduleListLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 20 {
		return 20
	}
	return limit
}

func scheduleListItemFromSchedule(taskSchedule TaskSchedule, referenceTime time.Time) ScheduleListItem {
	return ScheduleListItem{
		ScheduleID:      taskSchedule.TaskScheduleID,
		TaskInstruction: taskSchedule.Prompt,
		Description:     taskSchedule.Name,
		Cadence:         taskScheduleCadence(taskSchedule),
		CronExpression:  taskSchedule.CronExpression,
		RunAt:           taskSchedule.RunAt,
		Status:          taskScheduleStatus(taskSchedule, referenceTime),
		NextRunAt:       taskSchedule.NextRunAt,
		LastRunAt:       taskSchedule.LastRunAt,
	}
}

func taskScheduleCadence(taskSchedule TaskSchedule) string {
	switch taskSchedule.Kind {
	case TaskScheduleKindInterval:
		return "every " + strconv.Itoa(taskSchedule.IntervalSecond) + " seconds"
	case TaskScheduleKindCron:
		return "cron"
	default:
		return "once"
	}
}

func taskScheduleStatus(taskSchedule TaskSchedule, referenceTime time.Time) string {
	if taskSchedule.NextRunAt == nil || taskSchedule.ExpiresAt != nil && !taskSchedule.ExpiresAt.After(referenceTime) {
		return "expired"
	}
	if strings.TrimSpace(taskSchedule.LastError) != "" {
		return "failed"
	}
	return "active"
}
