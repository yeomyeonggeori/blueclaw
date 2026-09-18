package task

import (
	"strings"
	"time"
)

type ScheduleHintOutcome string

const (
	ScheduleHintResolved  ScheduleHintOutcome = "resolved"
	ScheduleHintAmbiguous ScheduleHintOutcome = "ambiguous"
	ScheduleHintNotFound  ScheduleHintOutcome = "not_found"
)

type ScheduleCandidate struct {
	ScheduleID  string     `json:"scheduleID"`
	Description string     `json:"description"`
	NextRunAt   *time.Time `json:"nextRunAt,omitempty"`
}

type ScheduleHintResolution struct {
	Outcome    ScheduleHintOutcome
	Match      Schedule
	Candidates []ScheduleCandidate
}

func OpenSchedulesCreatedBy(schedules []Schedule, creatorPersonID string, referenceTime time.Time) []Schedule {
	creator := strings.TrimSpace(creatorPersonID)
	own := []Schedule{}
	for _, schedule := range schedules {
		if strings.TrimSpace(schedule.CreatorPersonID) != creator {
			continue
		}
		if scheduleStatus(schedule, referenceTime) == "expired" {
			continue
		}
		own = append(own, schedule)
	}
	return own
}

func ResolveScheduleHint(hint string, schedules []Schedule) ScheduleHintResolution {
	trimmedHint := strings.TrimSpace(hint)
	if trimmedHint == "" {
		return ScheduleHintResolution{Outcome: ScheduleHintNotFound, Candidates: []ScheduleCandidate{}}
	}
	for _, schedule := range schedules {
		if strings.TrimSpace(schedule.ScheduleID) == trimmedHint {
			return ScheduleHintResolution{Outcome: ScheduleHintResolved, Match: schedule}
		}
	}
	if resolution, isDecided := resolutionFrom(schedulesWithDescriptionEqualTo(trimmedHint, schedules)); isDecided {
		return resolution
	}
	if resolution, isDecided := resolutionFrom(schedulesWithDescriptionContaining(trimmedHint, schedules)); isDecided {
		return resolution
	}
	return ScheduleHintResolution{Outcome: ScheduleHintNotFound, Candidates: []ScheduleCandidate{}}
}

func ScheduleCandidatesOf(schedules []Schedule) []ScheduleCandidate {
	candidates := make([]ScheduleCandidate, 0, len(schedules))
	for _, schedule := range schedules {
		candidates = append(candidates, ScheduleCandidate{
			ScheduleID:  schedule.ScheduleID,
			Description: schedule.Name,
			NextRunAt:   schedule.NextRunAt,
		})
	}
	return candidates
}

func resolutionFrom(matches []Schedule) (ScheduleHintResolution, bool) {
	switch len(matches) {
	case 0:
		return ScheduleHintResolution{}, false
	case 1:
		return ScheduleHintResolution{Outcome: ScheduleHintResolved, Match: matches[0]}, true
	default:
		return ScheduleHintResolution{Outcome: ScheduleHintAmbiguous, Candidates: ScheduleCandidatesOf(matches)}, true
	}
}

func schedulesWithDescriptionEqualTo(hint string, schedules []Schedule) []Schedule {
	matches := []Schedule{}
	for _, schedule := range schedules {
		if strings.EqualFold(strings.TrimSpace(schedule.Name), hint) {
			matches = append(matches, schedule)
		}
	}
	return matches
}

func schedulesWithDescriptionContaining(hint string, schedules []Schedule) []Schedule {
	foldedHint := strings.ToLower(hint)
	matches := []Schedule{}
	for _, schedule := range schedules {
		description := strings.ToLower(strings.TrimSpace(schedule.Name))
		if description != "" && strings.Contains(description, foldedHint) {
			matches = append(matches, schedule)
		}
	}
	return matches
}
