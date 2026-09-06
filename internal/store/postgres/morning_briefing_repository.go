package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

var errMorningBriefingScheduleIDConflict = errors.New("morning briefing schedule ID belongs to another schedule")
var errStaleMorningBriefingLease = errors.New("morning briefing schedule lease is no longer owned")

type managedMorningBriefingSchedule struct {
	taskScheduleID   string
	creatorPersonID  string
	name             string
	prompt           string
	executionMode    string
	agentProfileName string
	platform         string
	conversationID   string
	replyTargetID    string
	timeZone         string
	kind             string
	cronExpression   string
	nextRunAt        *time.Time
	leaseOwner       string
	leasedUntil      *time.Time
}

func (repository TaskScheduleRepository) ReconcileMorningBriefings(ctx context.Context, desired []task.TaskSchedule, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	transaction, errorValue := repository.database.SQL.BeginTx(ctx, nil)
	if errorValue != nil {
		return errorValue
	}
	defer transaction.Rollback()
	desiredPeople := make(map[string]struct{}, len(desired))
	for _, schedule := range desired {
		if schedule.CreatorPersonID == "" {
			return errors.New("morning briefing schedule has no person")
		}
		if _, exists := desiredPeople[schedule.CreatorPersonID]; exists {
			return fmt.Errorf("duplicate morning briefing schedule for person %s", schedule.CreatorPersonID)
		}
		desiredPeople[schedule.CreatorPersonID] = struct{}{}
		if errorValue := reconcileMorningBriefingSchedule(ctx, transaction, schedule, referenceTime); errorValue != nil {
			return errorValue
		}
	}
	if errorValue := deactivateMissingMorningBriefings(ctx, transaction, desiredPeople, referenceTime); errorValue != nil {
		return errorValue
	}
	return transaction.Commit()
}

func reconcileMorningBriefingSchedule(ctx context.Context, transaction *sql.Tx, desired task.TaskSchedule, referenceTime time.Time) error {
	existing, isFound, errorValue := findManagedMorningBriefingSchedule(ctx, transaction, desired.CreatorPersonID)
	if errorValue != nil {
		return errorValue
	}
	if !isFound {
		return insertManagedMorningBriefingSchedule(ctx, transaction, desired)
	}
	if existing.taskScheduleID != desired.TaskScheduleID {
		return errors.New("morning briefing person mapping has a different schedule ID")
	}
	if existing.leasedUntil != nil && existing.leasedUntil.After(referenceTime) {
		return nil
	}
	configurationChanged, cadenceChanged := morningBriefingConfigurationChanged(existing, desired)
	if !configurationChanged {
		return nil
	}
	nextRunAt := existing.nextRunAt
	if cadenceChanged {
		nextRunAt = desired.NextRunAt
	}
	_, errorValue = transaction.ExecContext(ctx, `
UPDATE task_schedule
SET name = $1, prompt = $2, execution_mode = $3, agent_profile_name = $4,
    schedule_kind = $5, cron_expression = $6, next_run_at = $7,
    platform = $8, delivery_conversation_id = $9, reply_target_id = $10,
    time_zone = $11, expires_at = CASE WHEN $7 IS NULL THEN expires_at ELSE NULL END, updated_at = $12
WHERE task_schedule_id = $13`,
		desired.Name, desired.Prompt, normalizedTaskScheduleExecutionMode(desired.ExecutionMode), desired.AgentProfileName,
		string(desired.Kind), emptyStringAsNil(desired.CronExpression), nextRunAt,
		desired.Platform, desired.ConversationID, desired.ReplyTargetID, desired.TimeZone, referenceTime,
		desired.TaskScheduleID)
	return errorValue
}

func (repository TaskScheduleRepository) RetireMorningBriefingOccurrence(schedule task.TaskSchedule, errorMessage string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	transaction, errorValue := repository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return errorValue
	}
	defer transaction.Rollback()
	if errorValue := verifyMorningBriefingLease(transaction, schedule, referenceTime); errorValue != nil {
		return errorValue
	}
	schedule.ExpiresAt = nil
	schedule, errorValue = (task.TaskScheduler{}).InitializeTaskSchedule(schedule, referenceTime)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = transaction.ExecContext(context.Background(), `
UPDATE task_schedule
SET expires_at = NULL, next_run_at = $1, lease_owner = '', leased_until = NULL,
    failure_count = 0, last_error = $2, next_attempt_at = $3, updated_at = $3
WHERE task_schedule_id = $4
  AND EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE task_schedule_id = $4)`,
		schedule.NextRunAt, errorMessage, referenceTime, schedule.TaskScheduleID)
	if errorValue != nil {
		return errorValue
	}
	return transaction.Commit()
}

func verifyMorningBriefingLease(transaction *sql.Tx, schedule task.TaskSchedule, referenceTime time.Time) error {
	var leaseOwner string
	var leasedUntil sql.NullTime
	errorValue := transaction.QueryRowContext(context.Background(), `
SELECT lease_owner, leased_until
FROM task_schedule
WHERE task_schedule_id = $1
  AND EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE task_schedule_id = $1)
FOR UPDATE`, schedule.TaskScheduleID).Scan(&leaseOwner, &leasedUntil)
	if errorValue != nil {
		return errorValue
	}
	if schedule.LeaseOwner == "" || schedule.LeasedUntil == nil || leaseOwner != schedule.LeaseOwner || !leasedUntil.Valid || !leasedUntil.Time.Equal(*schedule.LeasedUntil) || !leasedUntil.Time.After(referenceTime) {
		return errStaleMorningBriefingLease
	}
	return nil
}

func findManagedMorningBriefingSchedule(ctx context.Context, transaction *sql.Tx, personID string) (managedMorningBriefingSchedule, bool, error) {
	var schedule managedMorningBriefingSchedule
	var nextRunAt, leasedUntil sql.NullTime
	errorValue := transaction.QueryRowContext(ctx, `
SELECT s.task_schedule_id, s.creator_person_id, s.name, s.prompt, s.execution_mode,
       s.agent_profile_name, s.platform, s.delivery_conversation_id, s.reply_target_id,
       s.time_zone, s.schedule_kind, COALESCE(s.cron_expression, ''), s.next_run_at,
       s.lease_owner, s.leased_until
FROM morning_briefing_schedule m
JOIN task_schedule s ON s.task_schedule_id = m.task_schedule_id
WHERE m.person_id = $1
FOR UPDATE`, personID).Scan(
		&schedule.taskScheduleID, &schedule.creatorPersonID, &schedule.name, &schedule.prompt,
		&schedule.executionMode, &schedule.agentProfileName, &schedule.platform, &schedule.conversationID,
		&schedule.replyTargetID, &schedule.timeZone, &schedule.kind, &schedule.cronExpression,
		&nextRunAt, &schedule.leaseOwner, &leasedUntil)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return managedMorningBriefingSchedule{}, false, nil
	}
	if errorValue != nil {
		return managedMorningBriefingSchedule{}, false, errorValue
	}
	schedule.nextRunAt = nullableTaskScheduleTime(nextRunAt)
	schedule.leasedUntil = nullableTaskScheduleTime(leasedUntil)
	return schedule, true, nil
}

func insertManagedMorningBriefingSchedule(ctx context.Context, transaction *sql.Tx, schedule task.TaskSchedule) error {
	result, errorValue := transaction.ExecContext(ctx, `
INSERT INTO task_schedule (
  task_schedule_id, creator_person_id, name, prompt, execution_mode, agent_profile_name,
  schedule_kind, cron_expression, next_run_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone, next_attempt_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,$11,$12,$13,$14,$10)
ON CONFLICT (task_schedule_id) DO NOTHING`,
		schedule.TaskScheduleID, schedule.CreatorPersonID, schedule.Name, schedule.Prompt,
		normalizedTaskScheduleExecutionMode(schedule.ExecutionMode), schedule.AgentProfileName,
		string(schedule.Kind), emptyStringAsNil(schedule.CronExpression), schedule.NextRunAt,
		schedule.CreatedAt, schedule.Platform, schedule.ConversationID, schedule.ReplyTargetID, schedule.TimeZone)
	if errorValue != nil {
		return errorValue
	}
	rowsAffected, errorValue := result.RowsAffected()
	if errorValue != nil {
		return errorValue
	}
	if rowsAffected == 0 {
		return errMorningBriefingScheduleIDConflict
	}
	_, errorValue = transaction.ExecContext(ctx, `
INSERT INTO morning_briefing_schedule (person_id, task_schedule_id)
	VALUES ($1, $2)`, schedule.CreatorPersonID, schedule.TaskScheduleID)
	if errorValue != nil {
		return errorValue
	}
	return nil
}

func morningBriefingConfigurationChanged(existing managedMorningBriefingSchedule, desired task.TaskSchedule) (bool, bool) {
	cadenceChanged := existing.timeZone != desired.TimeZone || existing.kind != string(desired.Kind) ||
		existing.cronExpression != desired.CronExpression || (existing.nextRunAt == nil) != (desired.NextRunAt == nil)
	return existing.name != desired.Name || existing.prompt != desired.Prompt ||
		existing.executionMode != normalizedTaskScheduleExecutionMode(desired.ExecutionMode) ||
		existing.agentProfileName != desired.AgentProfileName || existing.platform != desired.Platform ||
		existing.conversationID != desired.ConversationID || existing.replyTargetID != desired.ReplyTargetID ||
		cadenceChanged, cadenceChanged
}

func deactivateMissingMorningBriefings(ctx context.Context, transaction *sql.Tx, desiredPeople map[string]struct{}, referenceTime time.Time) error {
	rows, errorValue := transaction.QueryContext(ctx, `SELECT person_id FROM morning_briefing_schedule FOR UPDATE`)
	if errorValue != nil {
		return errorValue
	}
	missingPeople := []string{}
	for rows.Next() {
		var personID string
		if errorValue := rows.Scan(&personID); errorValue != nil {
			return errorValue
		}
		if _, exists := desiredPeople[personID]; !exists {
			missingPeople = append(missingPeople, personID)
		}
	}
	if errorValue := rows.Err(); errorValue != nil {
		_ = rows.Close()
		return errorValue
	}
	if errorValue := rows.Close(); errorValue != nil {
		return errorValue
	}
	for _, personID := range missingPeople {
		if _, errorValue := transaction.ExecContext(ctx, `
UPDATE task_schedule s
SET next_run_at = NULL, updated_at = $1
FROM morning_briefing_schedule m
WHERE m.person_id = $2 AND s.task_schedule_id = m.task_schedule_id
  AND (s.leased_until IS NULL OR s.leased_until <= $1)`, referenceTime, personID); errorValue != nil {
			return errorValue
		}
	}
	return rows.Err()
}
