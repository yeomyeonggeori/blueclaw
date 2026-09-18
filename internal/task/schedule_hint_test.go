package task

import (
	"testing"
	"time"
)

func hintScheduleFixture(scheduleID string, creatorPersonID string, description string, nextRunAt *time.Time) Schedule {
	return Schedule{
		ScheduleID:      scheduleID,
		CreatorPersonID: creatorPersonID,
		Name:            description,
		Kind:            ScheduleKindInterval,
		IntervalSecond:  3600,
		NextRunAt:       nextRunAt,
	}
}

func TestOpenSchedulesCreatedByLeavesOutColleaguesAndExpiredSchedules(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	schedules := []Schedule{
		hintScheduleFixture("schedule-own", "person-이샘플", "내 점검", &nextRunAt),
		hintScheduleFixture("schedule-colleague", "person-박예시", "최견본 점검", &nextRunAt),
		hintScheduleFixture("schedule-expired", "person-이샘플", "지난 점검", nil),
	}

	own := OpenSchedulesCreatedBy(schedules, "person-이샘플", referenceTime)

	if len(own) != 1 || own[0].ScheduleID != "schedule-own" {
		t.Fatalf("unexpected own schedules: %+v", own)
	}
}

func TestResolveScheduleHintPrefersAnExactIdentifier(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	schedules := []Schedule{
		hintScheduleFixture("schedule-1", "person-이샘플", "주간 보고", &nextRunAt),
		hintScheduleFixture("schedule-2", "person-이샘플", "schedule-1", &nextRunAt),
	}

	resolution := ResolveScheduleHint("schedule-1", schedules)

	if resolution.Outcome != ScheduleHintResolved || resolution.Match.ScheduleID != "schedule-1" {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
}

func TestResolveScheduleHintTakesAUniquePartialDescription(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	schedules := []Schedule{
		hintScheduleFixture("schedule-1", "person-이샘플", "주간 보고 월요일", &nextRunAt),
		hintScheduleFixture("schedule-2", "person-이샘플", "일일 점검", &nextRunAt),
	}

	resolution := ResolveScheduleHint("주간", schedules)

	if resolution.Outcome != ScheduleHintResolved || resolution.Match.ScheduleID != "schedule-1" {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
}

func TestResolveScheduleHintPrefersAnExactDescriptionOverASharedPrefix(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	schedules := []Schedule{
		hintScheduleFixture("schedule-1", "person-이샘플", "주간 보고", &nextRunAt),
		hintScheduleFixture("schedule-2", "person-이샘플", "주간 보고 금요일", &nextRunAt),
	}

	resolution := ResolveScheduleHint("주간 보고", schedules)

	if resolution.Outcome != ScheduleHintResolved || resolution.Match.ScheduleID != "schedule-1" {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
}

func TestResolveScheduleHintAnswersSeveralMatchesWithCandidates(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	schedules := []Schedule{
		hintScheduleFixture("schedule-1", "person-이샘플", "주간 보고 월요일", &nextRunAt),
		hintScheduleFixture("schedule-2", "person-이샘플", "주간 보고 금요일", &nextRunAt),
	}

	resolution := ResolveScheduleHint("주간 보고", schedules)

	if resolution.Outcome != ScheduleHintAmbiguous || len(resolution.Candidates) != 2 {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
	if resolution.Candidates[0].ScheduleID != "schedule-1" || resolution.Candidates[0].NextRunAt == nil {
		t.Fatalf("unexpected candidates: %+v", resolution.Candidates)
	}
}

func TestResolveScheduleHintAnswersNothingWithNoCandidate(t *testing.T) {
	referenceTime := time.Now().UTC()
	nextRunAt := referenceTime.Add(time.Hour)
	schedules := []Schedule{hintScheduleFixture("schedule-1", "person-이샘플", "주간 보고", &nextRunAt)}

	for _, hint := range []string{"", "최견본 점검"} {
		resolution := ResolveScheduleHint(hint, schedules)
		if resolution.Outcome != ScheduleHintNotFound || len(resolution.Candidates) != 0 {
			t.Fatalf("hint %q resolution = %+v", hint, resolution)
		}
	}
}
