package app

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type approvedCallScheduler struct {
	repository      task.ApprovedCallScheduleRepository
	companyProvider func() agentcontract.CompanyContext
}

func (scheduler approvedCallScheduler) ScheduleApprovedCall(_ context.Context, request task.ApprovedCallScheduleRequest) (task.Schedule, error) {
	if strings.TrimSpace(request.TimeZone) == "" && scheduler.companyProvider != nil {
		request.TimeZone = scheduler.companyProvider().TimeZone
	}
	return task.CreateApprovedCallSchedule(scheduler.repository, request)
}
