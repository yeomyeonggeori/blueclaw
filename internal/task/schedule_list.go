package task

import (
	"strconv"
	"strings"
	"time"
)

const scheduleListPageSize = 20

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

func ScheduleListQuery(creatorPersonID string, referenceTime time.Time) ScheduleListRequest {
	return ScheduleListRequest{
		CreatorPersonID: strings.TrimSpace(creatorPersonID),
		IncludeExpired:  true,
		Page:            1,
		PageSize:        scheduleListPageSize,
		ReferenceTime:   referenceTime,
	}
}

func ProjectScheduleList(schedules []Schedule, input ScheduleListInput, referenceTime time.Time) ScheduleListOutput {
	limit := normalizedScheduleListLimit(input.Limit)
	status := strings.TrimSpace(input.Status)
	items := []ScheduleListItem{}
	for _, schedule := range schedules {
		item := scheduleListItemFromSchedule(schedule, referenceTime)
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
	if limit > scheduleListPageSize {
		return scheduleListPageSize
	}
	return limit
}

func scheduleListItemFromSchedule(schedule Schedule, referenceTime time.Time) ScheduleListItem {
	return ScheduleListItem{
		ScheduleID:      schedule.ScheduleID,
		TaskInstruction: schedule.Prompt,
		Description:     schedule.Name,
		Cadence:         scheduleCadence(schedule),
		CronExpression:  schedule.CronExpression,
		RunAt:           schedule.RunAt,
		Status:          scheduleStatus(schedule, referenceTime),
		NextRunAt:       schedule.NextRunAt,
		LastRunAt:       schedule.LastRunAt,
	}
}

func scheduleCadence(schedule Schedule) string {
	switch schedule.Kind {
	case ScheduleKindInterval:
		return "every " + strconv.Itoa(schedule.IntervalSecond) + " seconds"
	case ScheduleKindCron:
		return "cron"
	default:
		return "once"
	}
}

func scheduleStatus(schedule Schedule, referenceTime time.Time) string {
	if schedule.NextRunAt == nil || schedule.ExpiresAt != nil && !schedule.ExpiresAt.After(referenceTime) {
		return "expired"
	}
	if strings.TrimSpace(schedule.LastError) != "" {
		return "failed"
	}
	return "active"
}
