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
	Match      TaskSchedule
	Candidates []ScheduleCandidate
}

func OpenSchedulesCreatedBy(taskSchedules []TaskSchedule, creatorPersonID string, referenceTime time.Time) []TaskSchedule {
	creator := strings.TrimSpace(creatorPersonID)
	own := []TaskSchedule{}
	for _, taskSchedule := range taskSchedules {
		if strings.TrimSpace(taskSchedule.CreatorPersonID) != creator {
			continue
		}
		if taskScheduleStatus(taskSchedule, referenceTime) == "expired" {
			continue
		}
		own = append(own, taskSchedule)
	}
	return own
}

func ResolveScheduleHint(hint string, taskSchedules []TaskSchedule) ScheduleHintResolution {
	trimmedHint := strings.TrimSpace(hint)
	if trimmedHint == "" {
		return ScheduleHintResolution{Outcome: ScheduleHintNotFound, Candidates: []ScheduleCandidate{}}
	}
	for _, taskSchedule := range taskSchedules {
		if strings.TrimSpace(taskSchedule.TaskScheduleID) == trimmedHint {
			return ScheduleHintResolution{Outcome: ScheduleHintResolved, Match: taskSchedule}
		}
	}
	if resolution, isDecided := resolutionFrom(schedulesWithDescriptionEqualTo(trimmedHint, taskSchedules)); isDecided {
		return resolution
	}
	if resolution, isDecided := resolutionFrom(schedulesWithDescriptionContaining(trimmedHint, taskSchedules)); isDecided {
		return resolution
	}
	return ScheduleHintResolution{Outcome: ScheduleHintNotFound, Candidates: []ScheduleCandidate{}}
}

func ScheduleCandidatesOf(taskSchedules []TaskSchedule) []ScheduleCandidate {
	candidates := make([]ScheduleCandidate, 0, len(taskSchedules))
	for _, taskSchedule := range taskSchedules {
		candidates = append(candidates, ScheduleCandidate{
			ScheduleID:  taskSchedule.TaskScheduleID,
			Description: taskSchedule.Name,
			NextRunAt:   taskSchedule.NextRunAt,
		})
	}
	return candidates
}

func resolutionFrom(matches []TaskSchedule) (ScheduleHintResolution, bool) {
	switch len(matches) {
	case 0:
		return ScheduleHintResolution{}, false
	case 1:
		return ScheduleHintResolution{Outcome: ScheduleHintResolved, Match: matches[0]}, true
	default:
		return ScheduleHintResolution{Outcome: ScheduleHintAmbiguous, Candidates: ScheduleCandidatesOf(matches)}, true
	}
}

func schedulesWithDescriptionEqualTo(hint string, taskSchedules []TaskSchedule) []TaskSchedule {
	matches := []TaskSchedule{}
	for _, taskSchedule := range taskSchedules {
		if strings.EqualFold(strings.TrimSpace(taskSchedule.Name), hint) {
			matches = append(matches, taskSchedule)
		}
	}
	return matches
}

func schedulesWithDescriptionContaining(hint string, taskSchedules []TaskSchedule) []TaskSchedule {
	foldedHint := strings.ToLower(hint)
	matches := []TaskSchedule{}
	for _, taskSchedule := range taskSchedules {
		description := strings.ToLower(strings.TrimSpace(taskSchedule.Name))
		if description != "" && strings.Contains(description, foldedHint) {
			matches = append(matches, taskSchedule)
		}
	}
	return matches
}
