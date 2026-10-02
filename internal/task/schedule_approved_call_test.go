package task

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func approvedCallScheduleRequest(referenceTime time.Time) ApprovedCallScheduleRequest {
	return ApprovedCallScheduleRequest{
		Call: ScheduleApprovedCall{
			ToolName:         "host_update",
			ToolInput:        json.RawMessage(`{"targetVersion":"v2026.10.02.090000"}`),
			ApproverPersonID: "person-admin",
			ApprovedAt:       referenceTime,
		},
		StartsAt:      referenceTime.Add(6 * time.Hour),
		Prompt:        "update yourself tonight",
		Delivery:      ScheduleDeliveryBinding{Platform: "buzz", ConversationID: "conversation-1", ReplyTargetID: "message-1"},
		TimeZone:      "Asia/Seoul",
		ReferenceTime: referenceTime,
	}
}

func TestAnApprovedCallIsHeldByAOnceScheduleOfTheApprover(t *testing.T) {
	referenceTime := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	schedule, errorValue := BuildApprovedCallSchedule(approvedCallScheduleRequest(referenceTime))
	if errorValue != nil {
		t.Fatalf("build: %v", errorValue)
	}
	if schedule.Kind != ScheduleKindOnce || schedule.CreatorPersonID != "person-admin" || !schedule.CarriesAnApprovedCall() {
		t.Fatalf("built %+v, expected a once schedule of the approver carrying the call", schedule)
	}
	if schedule.NextRunAt == nil || !schedule.NextRunAt.Equal(referenceTime.Add(6*time.Hour)) {
		t.Fatalf("the schedule next runs at %v, expected the chosen time", schedule.NextRunAt)
	}
}

func TestAnApprovedCallIsNeverScheduledForATimeAlreadyPast(t *testing.T) {
	referenceTime := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	request := approvedCallScheduleRequest(referenceTime)
	request.StartsAt = referenceTime.Add(-time.Minute)
	if _, errorValue := BuildApprovedCallSchedule(request); !errors.Is(errorValue, ErrApprovedCallNeedsAFutureStart) {
		t.Fatalf("a past start built with %v", errorValue)
	}
}

func TestAScheduleCarryingAnApprovalCannotBeMadeRecurring(t *testing.T) {
	referenceTime := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	schedule, errorValue := BuildApprovedCallSchedule(approvedCallScheduleRequest(referenceTime))
	if errorValue != nil {
		t.Fatalf("build: %v", errorValue)
	}
	cron := string(ScheduleKindCron)
	expression := "0 3 * * *"
	unbounded := "unbounded"
	_, errorValue = ApplyScheduleUpdate(schedule, ScheduleUpdateInput{Kind: &cron, CronExpression: &expression, RepeatPolicy: &unbounded}, "Asia/Seoul", referenceTime)
	if !errors.Is(errorValue, ErrScheduleCarriesAnApproval) {
		t.Fatalf("turning the approved schedule into a daily one answered %v", errorValue)
	}
}

func TestAScheduleCarryingAnApprovalCannotBeMovedToAnotherTime(t *testing.T) {
	referenceTime := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	schedule, errorValue := BuildApprovedCallSchedule(approvedCallScheduleRequest(referenceTime))
	if errorValue != nil {
		t.Fatalf("build: %v", errorValue)
	}
	later := referenceTime.Add(48 * time.Hour).Format(time.RFC3339)
	if _, errorValue := ApplyScheduleUpdate(schedule, ScheduleUpdateInput{RunAt: &later}, "Asia/Seoul", referenceTime); !errors.Is(errorValue, ErrScheduleCarriesAnApproval) {
		t.Fatalf("moving the approved schedule answered %v", errorValue)
	}
}
