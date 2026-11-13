package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

var errBriefingScheduleIDConflict = errors.New("briefing schedule ID belongs to another schedule")
var errStaleBriefingLease = errors.New("briefing schedule lease is no longer owned")

type managedBriefingSchedule struct {
	scheduleID       string
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

func (repository ScheduleRepository) ReconcileBriefings(ctx context.Context, kind task.BriefingKind, desired []task.Schedule, unsettledPeople []string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	transaction, errorValue := repository.database.SQL.BeginTx(ctx, nil)
	if errorValue != nil {
		return errorValue
	}
	defer transaction.Rollback()
	keptPeople := make(map[string]struct{}, len(desired)+len(unsettledPeople))
	for _, personID := range unsettledPeople {
		keptPeople[personID] = struct{}{}
	}
	desiredPeople := make(map[string]struct{}, len(desired))
	for _, schedule := range desired {
		if schedule.CreatorPersonID == "" {
			return errors.New("briefing schedule has no person")
		}
		if _, exists := desiredPeople[schedule.CreatorPersonID]; exists {
			return fmt.Errorf("duplicate %s briefing schedule for person %s", kind, schedule.CreatorPersonID)
		}
		desiredPeople[schedule.CreatorPersonID] = struct{}{}
		keptPeople[schedule.CreatorPersonID] = struct{}{}
		if errorValue := reconcileBriefingSchedule(ctx, transaction, kind, schedule, referenceTime); errorValue != nil {
			return errorValue
		}
	}
	if errorValue := deactivateMissingBriefings(ctx, transaction, kind, keptPeople, referenceTime); errorValue != nil {
		return errorValue
	}
	return transaction.Commit()
}

func reconcileBriefingSchedule(ctx context.Context, transaction *sql.Tx, kind task.BriefingKind, desired task.Schedule, referenceTime time.Time) error {
	existing, isFound, errorValue := findManagedBriefingSchedule(ctx, transaction, kind, desired.CreatorPersonID)
	if errorValue != nil {
		return errorValue
	}
	if !isFound {
		return insertManagedBriefingSchedule(ctx, transaction, kind, desired)
	}
	if existing.scheduleID != desired.ScheduleID {
		return errors.New("briefing person mapping has a different schedule ID")
	}
	if existing.leasedUntil != nil && existing.leasedUntil.After(referenceTime) {
		return nil
	}
	configurationChanged, cadenceChanged := briefingConfigurationChanged(existing, desired)
	if !configurationChanged {
		return nil
	}
	nextRunAt := existing.nextRunAt
	if cadenceChanged {
		nextRunAt = desired.NextRunAt
	}
	_, errorValue = transaction.ExecContext(ctx, `
UPDATE schedule
SET name = $1, prompt = $2, execution_mode = $3, agent_profile_name = $4,
    schedule_kind = $5, cron_expression = $6, next_run_at = $7,
    platform = $8, delivery_conversation_id = $9, reply_target_id = $10,
    time_zone = $11, expires_at = CASE WHEN $7::timestamptz IS NULL THEN expires_at ELSE NULL END, updated_at = $12
WHERE schedule_id = $13`,
		desired.Name, desired.Prompt, normalizedScheduleExecutionMode(desired.ExecutionMode), desired.AgentProfileName,
		string(desired.Kind), emptyStringAsNil(desired.CronExpression), nextRunAt,
		desired.Platform, desired.ConversationID, desired.ReplyTargetID, desired.TimeZone, referenceTime,
		desired.ScheduleID)
	return errorValue
}

func (repository ScheduleRepository) RetireBriefingOccurrence(schedule task.Schedule, errorMessage string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	transaction, errorValue := repository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return errorValue
	}
	defer transaction.Rollback()
	if errorValue := verifyBriefingLease(transaction, schedule, referenceTime); errorValue != nil {
		return errorValue
	}
	schedule.ExpiresAt = nil
	schedule, errorValue = (task.Scheduler{}).InitializeSchedule(schedule, referenceTime)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = transaction.ExecContext(context.Background(), `
UPDATE schedule
SET expires_at = NULL, next_run_at = $1, lease_owner = '', leased_until = NULL,
    failure_count = 0, last_error = $2, next_attempt_at = $3, updated_at = $3
WHERE schedule_id = $4
  AND EXISTS (SELECT 1 FROM briefing_schedule WHERE schedule_id = $4)`,
		schedule.NextRunAt, errorMessage, referenceTime, schedule.ScheduleID)
	if errorValue != nil {
		return errorValue
	}
	return transaction.Commit()
}

func verifyBriefingLease(transaction *sql.Tx, schedule task.Schedule, referenceTime time.Time) error {
	var leaseOwner string
	var leasedUntil sql.NullTime
	errorValue := transaction.QueryRowContext(context.Background(), `
SELECT lease_owner, leased_until
FROM schedule
WHERE schedule_id = $1
  AND EXISTS (SELECT 1 FROM briefing_schedule WHERE schedule_id = $1)
FOR UPDATE`, schedule.ScheduleID).Scan(&leaseOwner, &leasedUntil)
	if errorValue != nil {
		return errorValue
	}
	if schedule.LeaseOwner == "" || schedule.LeasedUntil == nil || leaseOwner != schedule.LeaseOwner || !leasedUntil.Valid || !leasedUntil.Time.Equal(*schedule.LeasedUntil) || !leasedUntil.Time.After(referenceTime) {
		return errStaleBriefingLease
	}
	return nil
}

func findManagedBriefingSchedule(ctx context.Context, transaction *sql.Tx, kind task.BriefingKind, personID string) (managedBriefingSchedule, bool, error) {
	var schedule managedBriefingSchedule
	var nextRunAt, leasedUntil sql.NullTime
	errorValue := transaction.QueryRowContext(ctx, `
SELECT s.schedule_id, s.creator_person_id, s.name, s.prompt, s.execution_mode,
       s.agent_profile_name, s.platform, s.delivery_conversation_id, s.reply_target_id,
       s.time_zone, s.schedule_kind, COALESCE(s.cron_expression, ''), s.next_run_at,
       s.lease_owner, s.leased_until
FROM briefing_schedule m
JOIN schedule s ON s.schedule_id = m.schedule_id
WHERE m.person_id = $1 AND m.briefing_kind = $2
FOR UPDATE`, personID, string(kind)).Scan(
		&schedule.scheduleID, &schedule.creatorPersonID, &schedule.name, &schedule.prompt,
		&schedule.executionMode, &schedule.agentProfileName, &schedule.platform, &schedule.conversationID,
		&schedule.replyTargetID, &schedule.timeZone, &schedule.kind, &schedule.cronExpression,
		&nextRunAt, &schedule.leaseOwner, &leasedUntil)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return managedBriefingSchedule{}, false, nil
	}
	if errorValue != nil {
		return managedBriefingSchedule{}, false, errorValue
	}
	schedule.nextRunAt = nullableScheduleTime(nextRunAt)
	schedule.leasedUntil = nullableScheduleTime(leasedUntil)
	return schedule, true, nil
}

func insertManagedBriefingSchedule(ctx context.Context, transaction *sql.Tx, kind task.BriefingKind, schedule task.Schedule) error {
	result, errorValue := transaction.ExecContext(ctx, `
INSERT INTO schedule (
  schedule_id, creator_person_id, name, prompt, execution_mode, agent_profile_name,
  schedule_kind, cron_expression, next_run_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone, next_attempt_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,$11,$12,$13,$14,$10)
ON CONFLICT (schedule_id) DO NOTHING`,
		schedule.ScheduleID, schedule.CreatorPersonID, schedule.Name, schedule.Prompt,
		normalizedScheduleExecutionMode(schedule.ExecutionMode), schedule.AgentProfileName,
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
		return errBriefingScheduleIDConflict
	}
	_, errorValue = transaction.ExecContext(ctx, `
INSERT INTO briefing_schedule (person_id, briefing_kind, schedule_id)
	VALUES ($1, $2, $3)`, schedule.CreatorPersonID, string(kind), schedule.ScheduleID)
	if errorValue != nil {
		return errorValue
	}
	return nil
}

func briefingConfigurationChanged(existing managedBriefingSchedule, desired task.Schedule) (bool, bool) {
	cadenceChanged := existing.timeZone != desired.TimeZone || existing.kind != string(desired.Kind) ||
		existing.cronExpression != desired.CronExpression || (existing.nextRunAt == nil) != (desired.NextRunAt == nil)
	return existing.name != desired.Name || existing.prompt != desired.Prompt ||
		existing.executionMode != normalizedScheduleExecutionMode(desired.ExecutionMode) ||
		existing.agentProfileName != desired.AgentProfileName || existing.platform != desired.Platform ||
		existing.conversationID != desired.ConversationID || existing.replyTargetID != desired.ReplyTargetID ||
		cadenceChanged, cadenceChanged
}

func deactivateMissingBriefings(ctx context.Context, transaction *sql.Tx, kind task.BriefingKind, keptPeople map[string]struct{}, referenceTime time.Time) error {
	rows, errorValue := transaction.QueryContext(ctx, `SELECT person_id FROM briefing_schedule WHERE briefing_kind = $1 FOR UPDATE`, string(kind))
	if errorValue != nil {
		return errorValue
	}
	missingPeople := []string{}
	for rows.Next() {
		var personID string
		if errorValue := rows.Scan(&personID); errorValue != nil {
			return errorValue
		}
		if _, exists := keptPeople[personID]; !exists {
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
		if errorValue := deactivateBriefings(ctx, transaction, personID, referenceTime, kind); errorValue != nil {
			return errorValue
		}
	}
	return rows.Err()
}

func deactivateBriefings(ctx context.Context, transaction *sql.Tx, personID string, referenceTime time.Time, kinds ...task.BriefingKind) error {
	if len(kinds) == 0 {
		kinds = task.BriefingKinds
	}
	_, errorValue := transaction.ExecContext(ctx, `
UPDATE schedule s
SET next_run_at = NULL, updated_at = $1
FROM briefing_schedule m
WHERE m.person_id = $2 AND m.briefing_kind = ANY($3::text[]) AND s.schedule_id = m.schedule_id
  AND (s.leased_until IS NULL OR s.leased_until <= $1)`, referenceTime, personID, briefingKindNames(kinds))
	return errorValue
}

func briefingKindNames(kinds []task.BriefingKind) []string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return names
}
