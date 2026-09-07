package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestReconcileMorningBriefingsPersistsLifecycleAndGuardsGenericMutations(t *testing.T) {
	ctx := context.Background()
	database, cleanup := morningBriefingIntegrationDatabase(t, ctx)
	defer cleanup()
	var errorValue error
	repository := NewTaskScheduleRepository(database)
	personID := "morning-briefing-test-person"
	scheduleID := task.MorningBriefingScheduleID(personID)
	now := time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC)
	defer func() {
		_, _ = database.SQL.ExecContext(ctx, "DELETE FROM morning_briefing_schedule WHERE person_id = $1", personID)
		_, _ = database.SQL.ExecContext(ctx, "DELETE FROM task_schedule WHERE task_schedule_id = $1", scheduleID)
		_, _ = database.SQL.ExecContext(ctx, "DELETE FROM person WHERE person_id = $1", personID)
	}()
	_, errorValue = database.SQL.ExecContext(ctx, `
INSERT INTO person (person_id, display_name, security_level_name, security_level_rank, created_at, updated_at)
VALUES ($1, '이샘플', 'member', 1, $2, $2)`, personID, now)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	desired := morningBriefingTestSchedule(personID, scheduleID, timePointer(now.Add(time.Hour)))
	if errorValue := repository.ReconcileMorningBriefings(ctx, []task.TaskSchedule{desired}, now); errorValue != nil {
		t.Fatal(errorValue)
	}
	var storedNextRunAt time.Time
	if errorValue := database.SQL.QueryRowContext(ctx, "SELECT next_run_at FROM task_schedule WHERE task_schedule_id = $1", scheduleID).Scan(&storedNextRunAt); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !storedNextRunAt.Equal(*desired.NextRunAt) {
		t.Fatalf("expected initial next run %v, got %v", *desired.NextRunAt, storedNextRunAt)
	}
	if _, errorValue := database.SQL.ExecContext(ctx, "UPDATE task_schedule SET last_run_at = $1, completed_run_count = 4, failure_count = 2 WHERE task_schedule_id = $2", now, scheduleID); errorValue != nil {
		t.Fatal(errorValue)
	}
	desired.NextRunAt = timePointer(now.Add(2 * time.Hour))
	if errorValue := repository.ReconcileMorningBriefings(ctx, []task.TaskSchedule{desired}, now); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := database.SQL.QueryRowContext(ctx, "SELECT next_run_at FROM task_schedule WHERE task_schedule_id = $1", scheduleID).Scan(&storedNextRunAt); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !storedNextRunAt.Equal(*timePointer(now.Add(time.Hour))) {
		t.Fatalf("expected unchanged due time, got %v", storedNextRunAt)
	}
	if _, errorValue := repository.UpdateTaskSchedule(task.TaskScheduleUpdateRequest{TaskScheduleID: scheduleID, RequesterPersonID: personID}); errorValue != nil {
		t.Fatal(errorValue)
	}
	deleted, errorValue := repository.DeleteTaskSchedule(task.TaskScheduleDeleteRequest{TaskScheduleID: scheduleID, RequesterPersonID: personID})
	if errorValue != nil || deleted.IsFound {
		t.Fatalf("expected managed delete guard, got %+v (%v)", deleted, errorValue)
	}
	cancelled, errorValue := repository.CancelTaskSchedules(task.TaskScheduleCancelRequest{Scope: task.TaskScheduleCancelScopeMine, RequesterPersonID: personID, CancelledAt: now})
	if errorValue != nil || len(cancelled.TaskSchedules) != 0 {
		t.Fatalf("expected managed cancel guard, got %+v (%v)", cancelled, errorValue)
	}
	desired.NextRunAt = nil
	if errorValue := repository.ReconcileMorningBriefings(ctx, []task.TaskSchedule{desired}, now); errorValue != nil {
		t.Fatal(errorValue)
	}
	var isDisabled bool
	if errorValue := database.SQL.QueryRowContext(ctx, "SELECT next_run_at IS NULL FROM task_schedule WHERE task_schedule_id = $1", scheduleID).Scan(&isDisabled); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !isDisabled {
		t.Fatalf("expected disabled schedule to have no next run")
	}
	desired.NextRunAt = timePointer(now.Add(3 * time.Hour))
	if errorValue := repository.ReconcileMorningBriefings(ctx, []task.TaskSchedule{desired}, now); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := database.SQL.QueryRowContext(ctx, "SELECT next_run_at IS NOT NULL FROM task_schedule WHERE task_schedule_id = $1", scheduleID).Scan(&isDisabled); errorValue != nil || !isDisabled {
		t.Fatalf("expected re-enabled schedule, got active=%v (%v)", isDisabled, errorValue)
	}
	if _, errorValue := database.SQL.ExecContext(ctx, "UPDATE task_schedule SET expires_at = $1, next_run_at = NULL WHERE task_schedule_id = $2", now.Add(-time.Minute), scheduleID); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := repository.ReconcileMorningBriefings(ctx, []task.TaskSchedule{desired}, now); errorValue != nil {
		t.Fatal(errorValue)
	}
	var isUnexpired bool
	if errorValue := database.SQL.QueryRowContext(ctx, "SELECT expires_at IS NULL, next_run_at IS NOT NULL FROM task_schedule WHERE task_schedule_id = $1", scheduleID).Scan(&isUnexpired, &isDisabled); errorValue != nil || !isUnexpired || !isDisabled {
		t.Fatalf("expected expired schedule recovery, got unexpired=%v active=%v (%v)", isUnexpired, isDisabled, errorValue)
	}
	if errorValue := repository.UpsertTaskSchedule(desired); errorValue != errManagedTaskScheduleMutation {
		t.Fatalf("expected generic upsert guard, got %v", errorValue)
	}
	if errorValue := repository.ReconcileMorningBriefings(ctx, nil, now); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := database.SQL.QueryRowContext(ctx, "SELECT next_run_at IS NULL FROM task_schedule WHERE task_schedule_id = $1", scheduleID).Scan(&isDisabled); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !isDisabled {
		t.Fatalf("expected removed person schedule to stay disabled")
	}
	if _, errorValue := database.SQL.ExecContext(ctx, "UPDATE task_schedule SET lease_owner = 'new-worker', leased_until = $1, next_run_at = $2 WHERE task_schedule_id = $3", now.Add(time.Hour), now, scheduleID); errorValue != nil {
		t.Fatal(errorValue)
	}
	stale := desired
	stale.LeaseOwner = "old-worker"
	stale.LeasedUntil = timePointer(now.Add(30 * time.Minute))
	if errorValue := repository.RetireMorningBriefingOccurrence(stale, "old worker failure", now); errorValue != errStaleMorningBriefingLease {
		t.Fatalf("expected stale retirement rejection, got %v", errorValue)
	}
	leaseUntil := now.Add(time.Hour)
	if _, errorValue := database.SQL.ExecContext(ctx, "UPDATE task_schedule SET lease_owner = 'current-worker', leased_until = $1, next_run_at = $2, failure_count = 4, completed_run_count = 2 WHERE task_schedule_id = $3", leaseUntil, now, scheduleID); errorValue != nil {
		t.Fatal(errorValue)
	}
	claimed := desired
	claimed.NextRunAt = timePointer(now)
	claimed.LeaseOwner = "current-worker"
	claimed.LeasedUntil = &leaseUntil
	if errorValue := repository.RetireMorningBriefingOccurrence(claimed, "fifth failure", now); errorValue != nil {
		t.Fatal(errorValue)
	}
	var failureCount, completedRunCount int
	var nextDay time.Time
	var lastError string
	if errorValue := database.SQL.QueryRowContext(ctx, "SELECT next_run_at, failure_count, completed_run_count, last_error FROM task_schedule WHERE task_schedule_id = $1", scheduleID).Scan(&nextDay, &failureCount, &completedRunCount, &lastError); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !nextDay.After(now) || failureCount != 0 || completedRunCount != 2 || lastError != "fifth failure" {
		t.Fatalf("expected terminal occurrence retirement, got next=%v failures=%d completed=%d error=%q", nextDay, failureCount, completedRunCount, lastError)
	}
}

func morningBriefingIntegrationDatabase(t *testing.T, ctx context.Context) (Database, func()) {
	t.Helper()
	connectionString := os.Getenv("BLUECLAW_TEST_POSTGRES_URL")
	if connectionString == "" {
		t.Skip("BLUECLAW_TEST_POSTGRES_URL is not configured")
	}
	parsedURL, errorValue := url.Parse(connectionString)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	adminURL := *parsedURL
	adminURL.Path = "/postgres"
	adminDatabase, errorValue := OpenDatabase(ctx, adminURL.String())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())))
	databaseName := fmt.Sprintf("briefing_%x", digest[:6])
	if _, errorValue := adminDatabase.SQL.ExecContext(ctx, "CREATE DATABASE "+databaseName); errorValue != nil {
		adminDatabase.Close()
		t.Fatal(errorValue)
	}
	var database Database
	t.Cleanup(func() {
		database.Close()
		_, _ = adminDatabase.SQL.ExecContext(ctx, "DROP DATABASE "+databaseName)
		adminDatabase.Close()
	})
	databaseURL := *parsedURL
	databaseURL.Path = "/" + databaseName
	database, errorValue = OpenDatabase(ctx, databaseURL.String())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := (MigrationRunner{MigrationDirectoryPath: "../../../migrations"}).ApplyMigrations(ctx, database); errorValue != nil {
		database.Close()
		t.Fatal(errorValue)
	}
	return database, func() {}
}

func morningBriefingTestSchedule(personID string, scheduleID string, nextRunAt *time.Time) task.TaskSchedule {
	return task.TaskSchedule{
		TaskScheduleID: scheduleID, CreatorPersonID: personID, Name: "Morning briefing", Prompt: "brief",
		ExecutionMode: task.TaskScheduleExecutionModeAgent, AgentProfileName: "default", Platform: "mattermost",
		ConversationID: "conversation", ReplyTargetID: "reply", TimeZone: "Asia/Seoul", Kind: task.TaskScheduleKindCron,
		CronExpression: "0 8 * * *", NextRunAt: nextRunAt, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}
