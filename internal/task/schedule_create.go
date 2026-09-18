package task

import (
	"errors"
	"strconv"
	"strings"
)

const MaximumOpenScheduleCountPerPerson = 50

var ErrScheduleLimitReached = errors.New("a person can hold " + strconv.Itoa(MaximumOpenScheduleCountPerPerson) + " open schedules; cancel one before creating another")

type ScheduleCreateRepository interface {
	ListSchedules(ScheduleListRequest) (ScheduleListResult, error)
	UpsertSchedule(Schedule) error
}

func CreateSchedule(repository ScheduleCreateRepository, input ScheduleCreateInput, createContext ScheduleCreateContext) (Schedule, error) {
	schedule, errorValue := InitializeScheduleCreate(input, createContext)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	openScheduleCount, errorValue := openScheduleCountOf(repository, createContext)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	if openScheduleCount >= MaximumOpenScheduleCountPerPerson {
		return Schedule{}, ErrScheduleLimitReached
	}
	if errorValue := repository.UpsertSchedule(schedule); errorValue != nil {
		return Schedule{}, errorValue
	}
	return schedule, nil
}

func openScheduleCountOf(repository ScheduleCreateRepository, createContext ScheduleCreateContext) (int, error) {
	result, errorValue := repository.ListSchedules(ScheduleListRequest{
		CreatorPersonID: strings.TrimSpace(createContext.CreatorPersonID),
		Page:            1,
		PageSize:        1,
		ReferenceTime:   createContext.ReferenceTime,
	})
	if errorValue != nil {
		return 0, errorValue
	}
	return result.TotalCount, nil
}
