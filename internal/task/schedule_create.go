package task

import (
	"errors"
	"strconv"
	"strings"
)

const MaximumOpenScheduleCountPerPerson = 50

var ErrScheduleLimitReached = errors.New("a person can hold " + strconv.Itoa(MaximumOpenScheduleCountPerPerson) + " open schedules; cancel one before creating another")

type ScheduleCreateRepository interface {
	ListTaskSchedules(TaskScheduleListRequest) (TaskScheduleListResult, error)
	UpsertTaskSchedule(TaskSchedule) error
}

func CreateSchedule(repository ScheduleCreateRepository, input ScheduleCreateInput, createContext ScheduleCreateContext) (TaskSchedule, error) {
	taskSchedule, errorValue := InitializeScheduleCreate(input, createContext)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	openScheduleCount, errorValue := openScheduleCountOf(repository, createContext)
	if errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	if openScheduleCount >= MaximumOpenScheduleCountPerPerson {
		return TaskSchedule{}, ErrScheduleLimitReached
	}
	if errorValue := repository.UpsertTaskSchedule(taskSchedule); errorValue != nil {
		return TaskSchedule{}, errorValue
	}
	return taskSchedule, nil
}

func openScheduleCountOf(repository ScheduleCreateRepository, createContext ScheduleCreateContext) (int, error) {
	result, errorValue := repository.ListTaskSchedules(TaskScheduleListRequest{
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
