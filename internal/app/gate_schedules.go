package app

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type gateSchedules struct {
	repository      task.ScheduleRepository
	companyProvider func() agentcontract.CompanyContext
}

func (schedules gateSchedules) ScheduleApprovedCall(_ context.Context, request task.ApprovedCallScheduleRequest) (task.Schedule, error) {
	if strings.TrimSpace(request.TimeZone) == "" && schedules.companyProvider != nil {
		request.TimeZone = schedules.companyProvider().TimeZone
	}
	return task.CreateApprovedCallSchedule(schedules.repository, request)
}

func (schedules gateSchedules) ScheduleByID(scheduleID string) (task.Schedule, bool, error) {
	return schedules.repository.ScheduleByID(scheduleID)
}
