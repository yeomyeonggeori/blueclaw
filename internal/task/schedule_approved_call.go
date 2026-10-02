package task

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrScheduleCarriesAnApproval     = errors.New("this schedule carries an approved call, which runs exactly as it was approved; cancel it and ask again to change it")
	ErrApprovedCallNeedsAFutureStart = errors.New("an approved call is scheduled for a time that has not passed yet")
)

type ScheduleApprovedCall struct {
	ToolName         string          `json:"toolName"`
	ToolInput        json.RawMessage `json:"toolInput"`
	ApproverPersonID string          `json:"approverPersonID"`
	ApprovedAt       time.Time       `json:"approvedAt"`
	OriginTaskRunID  string          `json:"originTaskRunID,omitempty"`
}

type ApprovedCallScheduleRequest struct {
	Call             ScheduleApprovedCall
	StartsAt         time.Time
	Prompt           string
	AgentProfileName string
	Delivery         ScheduleDeliveryBinding
	TimeZone         string
	ReferenceTime    time.Time
}

func (schedule Schedule) CarriesAnApprovedCall() bool {
	return schedule.ApprovedCall != nil && strings.TrimSpace(schedule.ApprovedCall.ToolName) != ""
}

func BuildApprovedCallSchedule(request ApprovedCallScheduleRequest) (Schedule, error) {
	if !request.StartsAt.After(request.ReferenceTime) {
		return Schedule{}, ErrApprovedCallNeedsAFutureStart
	}
	call := request.Call
	call.ToolName = strings.TrimSpace(call.ToolName)
	startsAt := request.StartsAt.UTC()
	schedule, errorValue := BuildScheduleCreate(ScheduleCreateInput{
		Description:     call.ToolName,
		TaskInstruction: request.Prompt,
		Kind:            string(ScheduleKindOnce),
		RunAt:           startsAt.Format(time.RFC3339),
		TimeZone:        request.TimeZone,
	}, ScheduleCreateContext{
		CreatorPersonID:  call.ApproverPersonID,
		AgentProfileName: request.AgentProfileName,
		Delivery:         request.Delivery,
		CompanyTimeZone:  request.TimeZone,
		ReferenceTime:    request.ReferenceTime,
	})
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	schedule.ApprovedCall = &call
	return initializedScheduleWithFutureRun(schedule, request.ReferenceTime)
}

type ApprovedCallScheduleRepository interface {
	UpsertSchedule(Schedule) error
}

func CreateApprovedCallSchedule(repository ApprovedCallScheduleRepository, request ApprovedCallScheduleRequest) (Schedule, error) {
	schedule, errorValue := BuildApprovedCallSchedule(request)
	if errorValue != nil {
		return Schedule{}, errorValue
	}
	if errorValue := repository.UpsertSchedule(schedule); errorValue != nil {
		return Schedule{}, errorValue
	}
	return schedule, nil
}
