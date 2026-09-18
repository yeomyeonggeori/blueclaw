package postgres

import (
	"database/sql"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type scheduleScannerStub struct {
	values []any
}

func (scanner scheduleScannerStub) Scan(targets ...any) error {
	for index, value := range scanner.values {
		switch target := targets[index].(type) {
		case *string:
			*target = value.(string)
		case *bool:
			*target = value.(bool)
		case *int:
			*target = value.(int)
		case *time.Time:
			*target = value.(time.Time)
		case *sql.NullInt64:
			*target = value.(sql.NullInt64)
		case *sql.NullString:
			*target = value.(sql.NullString)
		case *sql.NullTime:
			*target = value.(sql.NullTime)
		}
	}
	return nil
}

func TestScanScheduleIncludesRunLimit(t *testing.T) {
	createdAt := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	schedule, errorValue := scanSchedule(scheduleScannerStub{values: []any{
		"schedule-1",
		"person-1",
		"limited reminder",
		"say sorry.",
		"default",
		"message",
		"interval",
		sql.NullTime{},
		sql.NullInt64{Int64: 60, Valid: true},
		sql.NullString{},
		sql.NullTime{},
		sql.NullTime{},
		"",
		sql.NullTime{Time: createdAt.Add(time.Hour), Valid: true},
		createdAt,
		createdAt,
		"mattermost",
		"channel-1",
		"reply-1",
		"Asia/Seoul",
		"",
		sql.NullTime{},
		0,
		"",
		sql.NullTime{},
		sql.NullInt64{Int64: 10, Valid: true},
		4,
	}})
	if errorValue != nil {
		t.Fatalf("expected task schedule scan to succeed: %v", errorValue)
	}
	if schedule.MaxRunCount != 10 || schedule.CompletedRunCount != 4 {
		t.Fatalf("expected run limit fields to scan, got %+v", schedule)
	}
	if schedule.ExecutionMode != "message" {
		t.Fatalf("expected execution mode to scan, got %+v", schedule)
	}
	if schedule.CronExpression != "" {
		t.Fatalf("expected nullable cron expression to scan as empty, got %q", schedule.CronExpression)
	}
}

func TestScheduleListFilterIncludesExpiredRowsWithoutNextRun(t *testing.T) {
	conditions, _ := scheduleListFilter(task.ScheduleListRequest{IncludeExpired: true})
	for _, condition := range conditions {
		if condition == "next_run_at IS NOT NULL" {
			t.Fatalf("expected includeExpired to allow schedules without next_run_at, got %+v", conditions)
		}
	}
}

func TestScheduleListFilterExcludesExpiredRowsByDefault(t *testing.T) {
	conditions, _ := scheduleListFilter(task.ScheduleListRequest{})
	if !containsScheduleListCondition(conditions, "next_run_at IS NOT NULL") || !containsScheduleListCondition(conditions, "(expires_at IS NULL OR expires_at > $1)") {
		t.Fatalf("expected default list filter to require active schedules, got %+v", conditions)
	}
}

func containsScheduleListCondition(conditions []string, expectedCondition string) bool {
	for _, condition := range conditions {
		if condition == expectedCondition {
			return true
		}
	}
	return false
}
