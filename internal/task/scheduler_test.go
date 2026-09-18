package task

import (
	"testing"
	"time"
)

func TestInitializeScheduleForOneTimeRun(t *testing.T) {
	scheduler := Scheduler{}
	runAt := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)

	schedule, errorValue := scheduler.InitializeSchedule(Schedule{
		ScheduleID: "schedule-1",
		Name:       "one-time",
		Kind:       ScheduleKindOnce,
		RunAt:      &runAt,
	}, time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected one-time task schedule to initialize: %v", errorValue)
	}
	if schedule.NextRunAt == nil {
		t.Fatal("expected one-time task schedule to include a next run time")
	}
	if !schedule.NextRunAt.Equal(runAt) {
		t.Fatalf("expected next run time to match scheduled time, got %s", schedule.NextRunAt.Format(time.RFC3339))
	}
}

func TestInitializeScheduleForIntervalRun(t *testing.T) {
	scheduler := Scheduler{}
	runAt := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)

	schedule, errorValue := scheduler.InitializeSchedule(Schedule{
		ScheduleID:     "schedule-2",
		Name:           "interval",
		Kind:           ScheduleKindInterval,
		RunAt:          &runAt,
		IntervalSecond: 900,
	}, time.Date(2026, 4, 23, 9, 7, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected interval task schedule to initialize: %v", errorValue)
	}
	if schedule.NextRunAt == nil {
		t.Fatal("expected interval task schedule to include a next run time")
	}

	expectedNextRunAt := time.Date(2026, 4, 23, 9, 15, 0, 0, time.UTC)
	if !schedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected interval next run time to be %s, got %s", expectedNextRunAt.Format(time.RFC3339), schedule.NextRunAt.Format(time.RFC3339))
	}
}

func TestInitializeScheduleForIntervalRunWithoutRunAtStartsNow(t *testing.T) {
	scheduler := Scheduler{}
	referenceTime := time.Date(2026, 4, 23, 9, 7, 0, 0, time.UTC)

	schedule, errorValue := scheduler.InitializeSchedule(Schedule{
		ScheduleID:     "schedule-immediate",
		Name:           "interval",
		Kind:           ScheduleKindInterval,
		IntervalSecond: 60,
	}, referenceTime)
	if errorValue != nil {
		t.Fatalf("expected interval task schedule to initialize: %v", errorValue)
	}
	if schedule.NextRunAt == nil || !schedule.NextRunAt.Equal(referenceTime) {
		t.Fatalf("expected interval next run time to start at %s, got %+v", referenceTime.Format(time.RFC3339), schedule.NextRunAt)
	}
}

func TestAdvanceScheduleStopsAtMaxRunCount(t *testing.T) {
	scheduler := Scheduler{}
	runAt := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)

	schedule, errorValue := scheduler.AdvanceSchedule(Schedule{
		ScheduleID:        "schedule-limited",
		Name:              "limited interval",
		Kind:              ScheduleKindInterval,
		RunAt:             &runAt,
		IntervalSecond:    60,
		MaxRunCount:       10,
		CompletedRunCount: 9,
	}, time.Date(2026, 4, 23, 9, 10, 0, 0, time.UTC))
	if errorValue != nil {
		t.Fatalf("expected limited interval schedule to advance: %v", errorValue)
	}
	if schedule.CompletedRunCount != 10 {
		t.Fatalf("expected completed run count to reach limit, got %+v", schedule)
	}
	if schedule.NextRunAt != nil {
		t.Fatalf("expected limited interval schedule to stop, got %+v", schedule.NextRunAt)
	}
}

func TestAdvanceScheduleForCronRun(t *testing.T) {
	scheduler := Scheduler{}
	executedAt := time.Date(2026, 4, 23, 10, 15, 0, 0, time.UTC)

	schedule, errorValue := scheduler.AdvanceSchedule(Schedule{
		ScheduleID:     "schedule-3",
		Name:           "cron",
		Kind:           ScheduleKindCron,
		CronExpression: "*/15 * * * *",
	}, executedAt)
	if errorValue != nil {
		t.Fatalf("expected cron task schedule to advance: %v", errorValue)
	}
	if schedule.NextRunAt == nil {
		t.Fatal("expected cron task schedule to include a next run time")
	}

	expectedNextRunAt := time.Date(2026, 4, 23, 10, 30, 0, 0, time.UTC)
	if !schedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected cron next run time to be %s, got %s", expectedNextRunAt.Format(time.RFC3339), schedule.NextRunAt.Format(time.RFC3339))
	}
	if schedule.LastRunAt == nil {
		t.Fatal("expected cron task schedule to include a last run time")
	}
	if !schedule.LastRunAt.Equal(executedAt) {
		t.Fatalf("expected last run time to be %s, got %s", executedAt.Format(time.RFC3339), schedule.LastRunAt.Format(time.RFC3339))
	}
}

func TestAdvanceScheduleForCronRunUsesScheduleTimeZone(t *testing.T) {
	scheduler := Scheduler{}
	executedAt := time.Date(2026, 5, 5, 22, 0, 0, 0, time.UTC)

	schedule, errorValue := scheduler.AdvanceSchedule(Schedule{
		ScheduleID:     "schedule-seoul-morning",
		Name:           "morning research",
		Kind:           ScheduleKindCron,
		CronExpression: "0 7 * * *",
		TimeZone:       "Asia/Seoul",
	}, executedAt)
	if errorValue != nil {
		t.Fatalf("expected cron task schedule to advance: %v", errorValue)
	}

	expectedNextRunAt := time.Date(2026, 5, 6, 22, 0, 0, 0, time.UTC)
	if schedule.NextRunAt == nil || !schedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected next run time to be %s, got %+v", expectedNextRunAt.Format(time.RFC3339), schedule.NextRunAt)
	}
}

func TestIsScheduleDue(t *testing.T) {
	scheduler := Scheduler{}
	nextRunAt := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)

	isDue := scheduler.IsScheduleDue(Schedule{
		ScheduleID: "schedule-4",
		Name:       "due",
		Kind:       ScheduleKindCron,
		NextRunAt:  &nextRunAt,
	}, time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC))
	if !isDue {
		t.Fatal("expected task schedule to be due at its next run time")
	}
}
