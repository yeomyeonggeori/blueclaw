package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestAnApprovedCallIsStoredWithItsScheduleAndOnlyOnAOnceSchedule(t *testing.T) {
	ctx := context.Background()
	database, cleanup := isolatedIntegrationDatabase(t, ctx)
	defer cleanup()
	repository := NewScheduleRepository(database)
	referenceTime := time.Now().UTC()
	schedule, errorValue := task.BuildApprovedCallSchedule(task.ApprovedCallScheduleRequest{
		Call: task.ScheduleApprovedCall{
			ToolName:  "host_update",
			ToolInput: json.RawMessage(`{"targetVersion":"v2026.10.02.090000"}`),
		},
		ApproverPersonID: "approved-call-test-admin",
		StartsAt:         referenceTime.Add(6 * time.Hour),
		Prompt:           "update yourself tonight",
		Delivery:         task.ScheduleDeliveryBinding{Platform: "buzz", ConversationID: "conversation-1", ReplyTargetID: "message-1"},
		TimeZone:         "Asia/Seoul",
		ReferenceTime:    referenceTime,
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer func() {
		_, _ = database.SQL.ExecContext(ctx, "DELETE FROM schedule WHERE schedule_id = $1", schedule.ScheduleID)
		_, _ = database.SQL.ExecContext(ctx, "DELETE FROM person WHERE person_id = $1", schedule.CreatorPersonID)
	}()
	if _, errorValue := database.SQL.ExecContext(ctx, `
INSERT INTO person (person_id, display_name, created_at, updated_at)
VALUES ($1, '이샘플', $2, $2)`, schedule.CreatorPersonID, referenceTime); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := repository.UpsertSchedule(schedule); errorValue != nil {
		t.Fatal(errorValue)
	}
	listed, errorValue := repository.ListSchedules(task.ScheduleListRequest{CreatorPersonID: "approved-call-test-admin", Page: 1, PageSize: 10, ReferenceTime: referenceTime})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(listed.Schedules) != 1 || !listed.Schedules[0].CarriesAnApprovedCall() || string(listed.Schedules[0].ApprovedCall.ToolInput) != `{"targetVersion": "v2026.10.02.090000"}` {
		t.Fatalf("the stored schedule reads back as %+v", listed.Schedules)
	}
	found, isFound, errorValue := repository.ScheduleByID(schedule.ScheduleID)
	if errorValue != nil || !isFound || found.ApprovedAt == nil || !found.ApprovedAt.Equal(referenceTime.Truncate(time.Microsecond)) {
		t.Fatalf("the schedule found by its ID reads back approved at %v (found %v, %v)", found.ApprovedAt, isFound, errorValue)
	}
	if _, isFound, errorValue := repository.ScheduleByID("no-such-schedule"); errorValue != nil || isFound {
		t.Fatalf("an unknown schedule ID was found %v with %v", isFound, errorValue)
	}
	_, errorValue = database.SQL.ExecContext(ctx, "UPDATE schedule SET schedule_kind = 'cron', run_at = NULL, cron_expression = '0 3 * * *' WHERE schedule_id = $1", schedule.ScheduleID)
	if errorValue == nil {
		t.Fatal("the database let a schedule carrying an approved call repeat")
	}
}
