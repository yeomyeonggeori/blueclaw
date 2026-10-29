package task

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func scheduleListFixture(count int, referenceTime time.Time) []Schedule {
	nextRunAt := referenceTime.Add(time.Hour)
	createdAt := referenceTime.Add(-time.Hour)
	schedules := []Schedule{}
	for index := range count {
		schedules = append(schedules, Schedule{
			ScheduleID: "schedule-" + strconv.Itoa(index),
			Prompt:     "instruction " + strconv.Itoa(index),
			Kind:       ScheduleKindOnce,
			NextRunAt:  &nextRunAt,
			CreatedAt:  createdAt.Add(-time.Duration(index/3) * time.Minute),
		})
	}
	return schedules
}

func readEverySchedulePage(t *testing.T, schedules []Schedule, input ScheduleListInput, referenceTime time.Time) ([]string, []ScheduleListOutput) {
	t.Helper()
	seen := []string{}
	pages := []ScheduleListOutput{}
	for {
		output, errorValue := ProjectScheduleList(schedules, input, referenceTime)
		if errorValue != nil {
			t.Fatalf("listing failed: %v", errorValue)
		}
		pages = append(pages, output)
		for _, item := range output.Schedules {
			seen = append(seen, item.ScheduleID)
		}
		if output.NextCursor == "" {
			return seen, pages
		}
		input.Cursor = output.NextCursor
	}
}

func TestScheduleListPagesReadEveryScheduleOnceNewestFirst(t *testing.T) {
	referenceTime := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	schedules := scheduleListFixture(7, referenceTime)

	seen, pages := readEverySchedulePage(t, schedules, ScheduleListInput{Limit: 3}, referenceTime)

	if len(pages) != 3 || len(seen) != 7 {
		t.Fatalf("seven schedules at three a page are three pages, got %d pages holding %v", len(pages), seen)
	}
	unique := map[string]bool{}
	for _, scheduleID := range seen {
		if unique[scheduleID] {
			t.Fatalf("%s came back twice across pages: %v", scheduleID, seen)
		}
		unique[scheduleID] = true
	}
	if seen[0] != "schedule-0" || seen[1] != "schedule-1" || seen[3] != "schedule-3" {
		t.Fatalf("newest first, ties broken by ID, got %v", seen)
	}
	for _, page := range pages {
		if page.TotalCount != 7 {
			t.Fatalf("every page states the whole count, got %d", page.TotalCount)
		}
	}
	if pages[len(pages)-1].NextCursor != "" {
		t.Fatalf("the last page carries no cursor, got %q", pages[len(pages)-1].NextCursor)
	}
}

func TestScheduleListCountsAndPagesOnlyTheRequestedStatus(t *testing.T) {
	referenceTime := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	schedules := scheduleListFixture(30, referenceTime)
	for index := range schedules {
		if index%3 == 0 {
			schedules[index].LastError = "temporary failure"
		}
	}

	seen, pages := readEverySchedulePage(t, schedules, ScheduleListInput{Status: "failed", Limit: 4}, referenceTime)

	if len(seen) != 10 || pages[0].TotalCount != 10 {
		t.Fatalf("ten failed schedules are all reachable and counted, got %v of %d", seen, pages[0].TotalCount)
	}
}

func TestScheduleListDefaultsToFiftyAndCapsAtOneHundred(t *testing.T) {
	referenceTime := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	schedules := scheduleListFixture(150, referenceTime)

	byDefault, _ := ProjectScheduleList(schedules, ScheduleListInput{}, referenceTime)
	atMost, _ := ProjectScheduleList(schedules, ScheduleListInput{Limit: 500}, referenceTime)

	if len(byDefault.Schedules) != 50 || byDefault.NextCursor == "" {
		t.Fatalf("an unset limit answers fifty and says more remain, got %d", len(byDefault.Schedules))
	}
	if len(atMost.Schedules) != 100 {
		t.Fatalf("a limit above one hundred answers one hundred, got %d", len(atMost.Schedules))
	}
}

func TestScheduleListRefusesACursorItNeverGave(t *testing.T) {
	referenceTime := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

	for _, cursor := range []string{"not-base64!", "e30", "bm90IGpzb24"} {
		_, errorValue := ProjectScheduleList(scheduleListFixture(3, referenceTime), ScheduleListInput{Cursor: cursor}, referenceTime)
		if !errors.Is(errorValue, ErrScheduleListCursor) {
			t.Fatalf("cursor %q should be refused, got %v", cursor, errorValue)
		}
	}
}
