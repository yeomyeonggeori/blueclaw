package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestAnApprovedCallIsSpentBeforeItsRunSoARestartNeverRunsItTwice(t *testing.T) {
	runAt := time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC)
	schedule := waitingSchedule(runAt)
	schedule.ApprovedCall = &task.ScheduleApprovedCall{
		ToolName:         "host_update",
		ToolInput:        json.RawMessage(`{"targetVersion":"v2026.10.02.090000"}`),
		ApproverPersonID: "person-1",
	}
	repository := &pollerScheduleRepository{schedules: []task.Schedule{schedule}}
	poller := SchedulePoller{
		ScheduleRepository:   repository,
		DeliveryRepository:   &pollerDeliveryRepository{},
		ScheduleRunner:       testScheduleRunner(task.TaskStatusWaitingApproval, ""),
		PersonAccessResolver: staticPersonAccessResolver{},
		TaskRunService:       task.NewTaskRunService(task.NewTaskEventService()),
	}

	if _, errorValue := poller.RunDue(context.Background(), runAt, 1); errorValue != nil {
		t.Fatal(errorValue)
	}

	if repository.succeeded == nil || repository.succeeded.NextRunAt != nil {
		t.Fatalf("the approved schedule was left %+v, so a daemon that dies mid-run claims it again", repository.succeeded)
	}
	if again, _ := repository.ClaimDueSchedules(1, time.Minute, runAt.Add(time.Hour), "blueclaw-app"); len(again) != 0 {
		t.Fatalf("the approved schedule is still claimable after its run started: %+v", again)
	}
}
