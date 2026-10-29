package task

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	scheduleListReadPageSize = 200
	scheduleListDefaultLimit = 50
	scheduleListMaximumLimit = 100
)

var ErrScheduleListCursor = errors.New("cursor is not one a previous schedule_list result gave")

type ScheduleListInput struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

type ScheduleListOutput struct {
	Schedules  []ScheduleListItem `json:"schedules"`
	TotalCount int                `json:"totalCount"`
	NextCursor string             `json:"nextCursor,omitempty"`
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

type scheduleListPosition struct {
	CreatedAt  time.Time `json:"createdAt"`
	ScheduleID string    `json:"scheduleID"`
}

func ScheduleListQuery(creatorPersonID string, referenceTime time.Time, page int) ScheduleListRequest {
	return ScheduleListRequest{
		CreatorPersonID: strings.TrimSpace(creatorPersonID),
		IncludeExpired:  true,
		Page:            page,
		PageSize:        scheduleListReadPageSize,
		ReferenceTime:   referenceTime,
	}
}

func ProjectScheduleList(schedules []Schedule, input ScheduleListInput, referenceTime time.Time) (ScheduleListOutput, error) {
	after, errorValue := decodedScheduleListPosition(input.Cursor)
	if errorValue != nil {
		return ScheduleListOutput{}, errorValue
	}
	matching := schedulesWithStatus(schedules, strings.TrimSpace(input.Status), referenceTime)
	sortSchedulesNewestFirst(matching)
	remaining := schedulesAfter(matching, after)
	limit := normalizedScheduleListLimit(input.Limit)
	page := remaining[:min(limit, len(remaining))]
	output := ScheduleListOutput{Schedules: []ScheduleListItem{}, TotalCount: len(matching)}
	for _, schedule := range page {
		output.Schedules = append(output.Schedules, scheduleListItemFromSchedule(schedule, referenceTime))
	}
	if len(remaining) > len(page) {
		output.NextCursor = encodedScheduleListPosition(page[len(page)-1])
	}
	return output, nil
}

func schedulesWithStatus(schedules []Schedule, status string, referenceTime time.Time) []Schedule {
	matching := []Schedule{}
	for _, schedule := range schedules {
		if status != "" && scheduleStatus(schedule, referenceTime) != status {
			continue
		}
		matching = append(matching, schedule)
	}
	return matching
}

func sortSchedulesNewestFirst(schedules []Schedule) {
	sort.SliceStable(schedules, func(left, right int) bool {
		return isScheduleListedBefore(schedules[left], scheduleListPosition{CreatedAt: schedules[right].CreatedAt, ScheduleID: schedules[right].ScheduleID})
	})
}

func isScheduleListedBefore(schedule Schedule, position scheduleListPosition) bool {
	if !schedule.CreatedAt.Equal(position.CreatedAt) {
		return schedule.CreatedAt.After(position.CreatedAt)
	}
	return schedule.ScheduleID < position.ScheduleID
}

func schedulesAfter(schedules []Schedule, after *scheduleListPosition) []Schedule {
	if after == nil {
		return schedules
	}
	for index, schedule := range schedules {
		if !isScheduleListedBefore(schedule, *after) && schedule.ScheduleID != after.ScheduleID {
			return schedules[index:]
		}
	}
	return []Schedule{}
}

func encodedScheduleListPosition(schedule Schedule) string {
	encoded, _ := json.Marshal(scheduleListPosition{CreatedAt: schedule.CreatedAt.UTC(), ScheduleID: schedule.ScheduleID})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodedScheduleListPosition(cursor string) (*scheduleListPosition, error) {
	trimmedCursor := strings.TrimSpace(cursor)
	if trimmedCursor == "" {
		return nil, nil
	}
	decoded, errorValue := base64.RawURLEncoding.DecodeString(trimmedCursor)
	if errorValue != nil {
		return nil, ErrScheduleListCursor
	}
	var position scheduleListPosition
	if errorValue := json.Unmarshal(decoded, &position); errorValue != nil || position.ScheduleID == "" || position.CreatedAt.IsZero() {
		return nil, ErrScheduleListCursor
	}
	return &position, nil
}

func normalizedScheduleListLimit(limit int) int {
	if limit <= 0 {
		return scheduleListDefaultLimit
	}
	return min(limit, scheduleListMaximumLimit)
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
