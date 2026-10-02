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
			ToolName:         "host_update",
			ToolInput:        json.RawMessage(`{"targetVersion":"v2026.10.02.090000"}`),
			ApproverPersonID: "approved-call-test-admin",
			ApprovedAt:       referenceTime,
		},
		StartsAt:      referenceTime.Add(6 * time.Hour),
		Prompt:        "update yourself tonight",
		Delivery:      task.ScheduleDeliveryBinding{Platform: "buzz", ConversationID: "conversation-1", ReplyTargetID: "message-1"},
		TimeZone:      "Asia/Seoul",
		ReferenceTime: referenceTime,
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer func() {
		_, _ = database.SQL.ExecContext(ctx, "DELETE FROM schedule WHERE schedule_id = $1", schedule.ScheduleID)
		_, _ = database.SQL.ExecContext(ctx, "DELETE FROM person WHERE person_id = $1", schedule.CreatorPersonID)
	}()
	if _, errorValue := database.SQL.ExecContext(ctx, `
INSERT INTO person (person_id, display_name, security_level_name, security_level_rank, created_at, updated_at)
VALUES ($1, '이샘플', 'member', 1, $2, $2)`, schedule.CreatorPersonID, referenceTime); errorValue != nil {
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
	_, errorValue = database.SQL.ExecContext(ctx, "UPDATE schedule SET schedule_kind = 'cron', run_at = NULL, cron_expression = '0 3 * * *' WHERE schedule_id = $1", schedule.ScheduleID)
	if errorValue == nil {
		t.Fatal("the database let a schedule carrying an approved call repeat")
	}
}
