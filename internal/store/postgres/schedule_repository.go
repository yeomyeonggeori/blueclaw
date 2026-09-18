package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

var errManagedScheduleMutation = errors.New("managed morning briefing schedules cannot be changed by generic schedule operations")

type ScheduleRepository struct {
	database Database
}

func NewScheduleRepository(database Database) ScheduleRepository {
	return ScheduleRepository{database: database}
}

func (scheduleRepository ScheduleRepository) UpsertSchedule(schedule task.Schedule) error {
	var isManaged bool
	if errorValue := scheduleRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE schedule_id = $1)`, schedule.ScheduleID).Scan(&isManaged); errorValue != nil {
		return errorValue
	}
	if isManaged {
		return errManagedScheduleMutation
	}
	now := time.Now().UTC()
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = now
	}
	if schedule.UpdatedAt.IsZero() {
		schedule.UpdatedAt = now
	}
	_, errorValue := scheduleRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO schedule (
  schedule_id, creator_person_id, name, prompt, execution_mode, agent_profile_name,
  schedule_kind, run_at, interval_second, cron_expression, next_run_at,
  last_run_at, last_task_run_id, expires_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone,
  lease_owner, leased_until, failure_count, last_error, next_attempt_at,
  max_run_count, completed_run_count
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
ON CONFLICT (schedule_id) DO UPDATE SET
  name = EXCLUDED.name,
  prompt = EXCLUDED.prompt,
  execution_mode = EXCLUDED.execution_mode,
  agent_profile_name = EXCLUDED.agent_profile_name,
  schedule_kind = EXCLUDED.schedule_kind,
  run_at = EXCLUDED.run_at,
  interval_second = EXCLUDED.interval_second,
  cron_expression = EXCLUDED.cron_expression,
  next_run_at = EXCLUDED.next_run_at,
  last_run_at = EXCLUDED.last_run_at,
  last_task_run_id = EXCLUDED.last_task_run_id,
  expires_at = EXCLUDED.expires_at,
  updated_at = EXCLUDED.updated_at,
  platform = EXCLUDED.platform,
  delivery_conversation_id = EXCLUDED.delivery_conversation_id,
  reply_target_id = EXCLUDED.reply_target_id,
  time_zone = EXCLUDED.time_zone,
  lease_owner = EXCLUDED.lease_owner,
  leased_until = EXCLUDED.leased_until,
  failure_count = EXCLUDED.failure_count,
  last_error = EXCLUDED.last_error,
  next_attempt_at = EXCLUDED.next_attempt_at,
  max_run_count = EXCLUDED.max_run_count,
  completed_run_count = EXCLUDED.completed_run_count
WHERE NOT EXISTS (
  SELECT 1 FROM morning_briefing_schedule
  WHERE schedule_id = schedule.schedule_id
)`,
		schedule.ScheduleID,
		emptyStringAsNil(schedule.CreatorPersonID),
		schedule.Name,
		schedule.Prompt,
		normalizedScheduleExecutionMode(schedule.ExecutionMode),
		schedule.AgentProfileName,
		string(schedule.Kind),
		schedule.RunAt,
		zeroAsNil(schedule.IntervalSecond),
		emptyStringAsNil(schedule.CronExpression),
		schedule.NextRunAt,
		schedule.LastRunAt,
		emptyStringAsNil(schedule.LastTaskRunID),
		scheduleExpiresAt(schedule),
		schedule.CreatedAt,
		schedule.UpdatedAt,
		schedule.Platform,
		schedule.ConversationID,
		schedule.ReplyTargetID,
		schedule.TimeZone,
		schedule.LeaseOwner,
		schedule.LeasedUntil,
		schedule.FailureCount,
		schedule.LastError,
		firstNonNilScheduleTime(schedule.NextAttemptAt, schedule.CreatedAt),
		zeroAsNil(schedule.MaxRunCount),
		schedule.CompletedRunCount,
	)
	return errorValue
}

func (scheduleRepository ScheduleRepository) UpdateSchedule(request task.ScheduleUpdateRequest) (task.ScheduleUpdateResult, error) {
	scheduleID := strings.TrimSpace(request.ScheduleID)
	requesterPersonID := strings.TrimSpace(request.RequesterPersonID)
	if scheduleID == "" || requesterPersonID == "" {
		return task.ScheduleUpdateResult{}, nil
	}
	transaction, errorValue := scheduleRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return task.ScheduleUpdateResult{}, errorValue
	}
	row := transaction.QueryRowContext(context.Background(), `SELECT `+scheduleReturningColumns()+`
FROM schedule
WHERE schedule_id = $1
  AND NOT EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE schedule_id = schedule.schedule_id)
  AND creator_person_id = $2
  AND next_run_at IS NOT NULL
  AND (expires_at IS NULL OR expires_at > now())
FOR UPDATE`, scheduleID, requesterPersonID)
	schedule, errorValue := scanSchedule(row)
	if errors.Is(errorValue, sql.ErrNoRows) {
		_ = transaction.Rollback()
		return task.ScheduleUpdateResult{}, nil
	}
	if errorValue != nil {
		_ = transaction.Rollback()
		return task.ScheduleUpdateResult{}, errorValue
	}
	if request.UpdateSchedule != nil {
		schedule, errorValue = request.UpdateSchedule(schedule)
		if errorValue != nil {
			_ = transaction.Rollback()
			return task.ScheduleUpdateResult{}, errorValue
		}
	}
	if errorValue := updateScheduleWithTransaction(transaction, schedule); errorValue != nil {
		_ = transaction.Rollback()
		return task.ScheduleUpdateResult{}, errorValue
	}
	if errorValue := transaction.Commit(); errorValue != nil {
		return task.ScheduleUpdateResult{}, errorValue
	}
	return task.ScheduleUpdateResult{Schedule: schedule, IsFound: true}, nil
}

func (scheduleRepository ScheduleRepository) DeleteSchedule(request task.ScheduleDeleteRequest) (task.ScheduleDeleteResult, error) {
	scheduleID := strings.TrimSpace(request.ScheduleID)
	requesterPersonID := strings.TrimSpace(request.RequesterPersonID)
	if scheduleID == "" || requesterPersonID == "" {
		return task.ScheduleDeleteResult{}, nil
	}
	row := scheduleRepository.database.SQL.QueryRowContext(context.Background(), `
DELETE FROM schedule
WHERE schedule_id = $1
  AND NOT EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE schedule_id = schedule.schedule_id)
  AND creator_person_id = $2
RETURNING `+scheduleReturningColumns(), scheduleID, requesterPersonID)
	schedule, errorValue := scanSchedule(row)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return task.ScheduleDeleteResult{}, nil
	}
	if errorValue != nil {
		return task.ScheduleDeleteResult{}, errorValue
	}
	return task.ScheduleDeleteResult{Schedule: schedule, IsFound: true}, nil
}

func (scheduleRepository ScheduleRepository) ClaimDueSchedules(limit int, leaseDuration time.Duration, referenceTime time.Time, leaseOwner string) ([]task.Schedule, error) {
	if limit <= 0 {
		limit = 1
	}
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	leaseSecond := int(leaseDuration.Seconds())
	if leaseSecond <= 0 {
		leaseSecond = 300
	}
	rows, errorValue := scheduleRepository.database.SQL.QueryContext(context.Background(), `
WITH claim AS (
  SELECT schedule_id FROM schedule
  WHERE next_run_at IS NOT NULL
    AND next_run_at <= $1
    AND next_attempt_at <= $1
    AND (expires_at IS NULL OR expires_at > $1)
    AND (leased_until IS NULL OR leased_until <= $1 OR lease_owner = $3)
  ORDER BY next_run_at ASC
  LIMIT $2
  FOR UPDATE SKIP LOCKED
)
UPDATE schedule
SET lease_owner = $3,
  leased_until = $1 + ($4 * interval '1 second'),
  updated_at = $1
WHERE schedule_id IN (SELECT schedule_id FROM claim)
RETURNING `+scheduleReturningColumns(),
		referenceTime,
		limit,
		leaseOwner,
		leaseSecond,
	)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanSchedules(rows)
}

func (scheduleRepository ScheduleRepository) MarkScheduleSucceeded(schedule task.Schedule) error {
	now := time.Now().UTC()
	query := `
UPDATE schedule
SET next_run_at = $2,
  last_run_at = $3,
  last_task_run_id = $4,
  lease_owner = '',
  leased_until = NULL,
  failure_count = 0,
  last_error = '',
  next_attempt_at = $1,
  completed_run_count = $6,
  updated_at = $1

WHERE schedule_id = $5`
	arguments := []any{
		now,
		schedule.NextRunAt,
		schedule.LastRunAt,
		emptyStringAsNil(schedule.LastTaskRunID),
		schedule.ScheduleID,
		schedule.CompletedRunCount,
	}
	if task.IsMorningBriefing(schedule) {
		query += " AND EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE schedule_id = $5) AND lease_owner = $7 AND leased_until = $8"
		arguments = append(arguments, schedule.LeaseOwner, schedule.LeasedUntil)
	}
	result, errorValue := scheduleRepository.database.SQL.ExecContext(context.Background(), query, arguments...)
	if errorValue != nil {
		return errorValue
	}
	rowsAffected, errorValue := result.RowsAffected()
	if errorValue != nil {
		return errorValue
	}
	if task.IsMorningBriefing(schedule) && rowsAffected == 0 {
		return errStaleMorningBriefingLease
	}
	return nil
}

func (scheduleRepository ScheduleRepository) MarkScheduleSucceededAndEnqueueDelivery(schedule task.Schedule, taskRunID string, deliveryDeduplicationKey string, reply connectors.OutboundReply) (string, error) {
	deliveryDeduplicationKey = strings.TrimSpace(deliveryDeduplicationKey)
	if deliveryDeduplicationKey == "" {
		return "", errors.New("scheduled task delivery deduplication key is required")
	}
	now := time.Now().UTC()
	transaction, errorValue := scheduleRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return "", errorValue
	}
	defer transaction.Rollback()
	if task.IsMorningBriefing(schedule) {
		if errorValue := verifyMorningBriefingLease(transaction, schedule, now); errorValue != nil {
			return "", errorValue
		}
	}
	conversationID, errorValue := ensureConversationWithTransaction(transaction, schedule.Platform, schedule.ConversationID, now)
	if errorValue != nil {
		return "", errorValue
	}
	if errorValue := enqueueScheduledConnectorReplyWithTransaction(transaction, schedule, taskRunID, deliveryDeduplicationKey, conversationID, reply, now); errorValue != nil {
		return "", errorValue
	}
	if errorValue := markScheduleSucceededWithTransaction(transaction, schedule, now); errorValue != nil {
		return "", errorValue
	}
	if errorValue := transaction.Commit(); errorValue != nil {
		return "", errorValue
	}
	return deliveryDeduplicationKey, nil
}

func ensureConversationWithTransaction(transaction *sql.Tx, platform string, externalConversationID string, now time.Time) (string, error) {
	conversationID := platform + ":" + externalConversationID
	_, errorValue := transaction.ExecContext(context.Background(), `
INSERT INTO conversation (
  conversation_id, platform, external_conversation_id, conversation_type, display_name,
  last_seen_at, created_at, updated_at
) VALUES ($1,$2,$3,'opaque',$3,$4,$4,$4)
ON CONFLICT (platform, external_conversation_id) DO UPDATE SET
  last_seen_at = EXCLUDED.last_seen_at,
  updated_at = EXCLUDED.updated_at`,
		conversationID,
		platform,
		externalConversationID,
		now,
	)
	return conversationID, errorValue
}

func enqueueScheduledConnectorReplyWithTransaction(transaction *sql.Tx, schedule task.Schedule, taskRunID string, deliveryDeduplicationKey string, conversationID string, reply connectors.OutboundReply, now time.Time) error {
	reply = scheduledConnectorReply(taskRunID, deliveryDeduplicationKey, reply)
	replyTarget := connectors.ReplyTarget{
		ConversationID: schedule.ConversationID,
		ReplyTargetID:  schedule.ReplyTargetID,
		DedupeKey:      deliveryDeduplicationKey,
	}
	replyTargetDocument, errorValue := json.Marshal(replyTarget)
	if errorValue != nil {
		return errorValue
	}
	replyDocument, errorValue := json.Marshal(reply)
	if errorValue != nil {
		return errorValue
	}
	contentHash := sha256.Sum256([]byte(schedule.Prompt))
	_, errorValue = transaction.ExecContext(context.Background(), `
INSERT INTO raw_event (
  raw_event_id, platform, conversation_id, external_message_id, event_type,
  content_ciphertext, encryption_key_version, content_sha256, security_level_rank,
  required_classes, occurred_at, ingested_at, expires_at,
  reply_target_id, visible_context_ciphertext, visible_context_sha256, has_more_before, history_cursor
) VALUES ($1,$2,$3,$1,'scheduled_task',$4,1,$5,0,'{}',$6,$6,$7,$8,$9,$10,false,NULL)
ON CONFLICT (raw_event_id) DO NOTHING`,
		deliveryDeduplicationKey,
		schedule.Platform,
		conversationID,
		[]byte(schedule.Prompt),
		contentHash[:],
		now,
		now.AddDate(0, 0, 60),
		schedule.ReplyTargetID,
		mustJSON(connectors.VisibleContext{}),
		hashJSON(connectors.VisibleContext{}),
	)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = transaction.ExecContext(context.Background(), `
INSERT INTO connector_outbox (
  outbox_id, raw_event_id, platform, reply_target_id, reply_target_json, reply_json
) VALUES ($1,$1,$2,$3,$4,$5)
ON CONFLICT (outbox_id) DO NOTHING`,
		deliveryDeduplicationKey,
		schedule.Platform,
		schedule.ReplyTargetID,
		replyTargetDocument,
		replyDocument,
	)
	return errorValue
}

func scheduledConnectorReply(taskRunID string, deliveryDeduplicationKey string, reply connectors.OutboundReply) connectors.OutboundReply {
	reply.RawEventID = deliveryDeduplicationKey
	reply.OutboxID = deliveryDeduplicationKey
	reply.TaskRunID = firstNonEmptyPostgresString(reply.TaskRunID, taskRunID)
	reply.ReplyKind = firstNonEmptyPostgresString(reply.ReplyKind, "success")
	return reply
}

func markScheduleSucceededWithTransaction(transaction *sql.Tx, schedule task.Schedule, now time.Time) error {
	_, errorValue := transaction.ExecContext(context.Background(), `
UPDATE schedule
SET next_run_at = $2,
  last_run_at = $3,
  last_task_run_id = $4,
  lease_owner = '',
  leased_until = NULL,
  failure_count = 0,
  last_error = '',
  next_attempt_at = $1,
  completed_run_count = $6,
  updated_at = $1
WHERE schedule_id = $5`,
		now,
		schedule.NextRunAt,
		schedule.LastRunAt,
		emptyStringAsNil(schedule.LastTaskRunID),
		schedule.ScheduleID,
		schedule.CompletedRunCount,
	)
	return errorValue
}

func updateScheduleWithTransaction(transaction *sql.Tx, schedule task.Schedule) error {
	_, errorValue := transaction.ExecContext(context.Background(), `
UPDATE schedule
SET name = $2,
  prompt = $3,
  execution_mode = $4,
  agent_profile_name = $5,
  schedule_kind = $6,
  run_at = $7,
  interval_second = $8,
  cron_expression = $9,
  next_run_at = $10,
  last_run_at = $11,
  last_task_run_id = $12,
  expires_at = $13,
  updated_at = $14,
  platform = $15,
  delivery_conversation_id = $16,
  reply_target_id = $17,
  time_zone = $18,
  lease_owner = $19,
  leased_until = $20,
  failure_count = $21,
  last_error = $22,
  next_attempt_at = $23,
  max_run_count = $24,
  completed_run_count = $25
WHERE schedule_id = $1`,
		schedule.ScheduleID,
		schedule.Name,
		schedule.Prompt,
		normalizedScheduleExecutionMode(schedule.ExecutionMode),
		schedule.AgentProfileName,
		string(schedule.Kind),
		schedule.RunAt,
		zeroAsNil(schedule.IntervalSecond),
		emptyStringAsNil(schedule.CronExpression),
		schedule.NextRunAt,
		schedule.LastRunAt,
		emptyStringAsNil(schedule.LastTaskRunID),
		scheduleExpiresAt(schedule),
		schedule.UpdatedAt,
		schedule.Platform,
		schedule.ConversationID,
		schedule.ReplyTargetID,
		schedule.TimeZone,
		schedule.LeaseOwner,
		schedule.LeasedUntil,
		schedule.FailureCount,
		schedule.LastError,
		firstNonNilScheduleTime(schedule.NextAttemptAt, schedule.UpdatedAt),
		zeroAsNil(schedule.MaxRunCount),
		schedule.CompletedRunCount,
	)
	return errorValue
}

func (scheduleRepository ScheduleRepository) MarkScheduleFailed(schedule task.Schedule, errorMessage string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	nextAttemptAt := referenceTime.Add(scheduleRetryDelay(schedule.FailureCount + 1))
	query := `
UPDATE schedule
SET lease_owner = '',
  leased_until = NULL,
  failure_count = failure_count + 1,
  last_error = $1,
  next_attempt_at = $2,
  updated_at = $3

WHERE schedule_id = $4`
	arguments := []any{
		errorMessage,
		nextAttemptAt,
		referenceTime,
		schedule.ScheduleID,
	}
	if task.IsMorningBriefing(schedule) {
		query += " AND EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE schedule_id = $4) AND lease_owner = $5 AND leased_until = $6"
		arguments = append(arguments, schedule.LeaseOwner, schedule.LeasedUntil)
	}
	result, errorValue := scheduleRepository.database.SQL.ExecContext(context.Background(), query, arguments...)
	if errorValue != nil {
		return errorValue
	}
	rowsAffected, errorValue := result.RowsAffected()
	if errorValue != nil {
		return errorValue
	}
	if task.IsMorningBriefing(schedule) && rowsAffected == 0 {
		return errStaleMorningBriefingLease
	}
	return nil
}

func (scheduleRepository ScheduleRepository) ExpireSchedule(schedule task.Schedule, errorMessage string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	_, errorValue := scheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE schedule
SET expires_at = $1,
  next_run_at = NULL,
  lease_owner = '',
  leased_until = NULL,
  failure_count = failure_count + 1,
  last_error = $2,
  next_attempt_at = $1,
  updated_at = $1
WHERE schedule_id = $3`,
		referenceTime,
		errorMessage,
		schedule.ScheduleID,
	)
	return errorValue
}

func (scheduleRepository ScheduleRepository) CancelSchedules(request task.ScheduleCancelRequest) (task.ScheduleCancelResult, error) {
	cancelledAt := request.CancelledAt
	if cancelledAt.IsZero() {
		cancelledAt = time.Now().UTC()
	}
	requesterPersonID := strings.TrimSpace(request.RequesterPersonID)
	if requesterPersonID == "" {
		return task.ScheduleCancelResult{}, nil
	}
	conditions := []string{
		"next_run_at IS NOT NULL",
		"(expires_at IS NULL OR expires_at > $1)",
		"NOT EXISTS (SELECT 1 FROM morning_briefing_schedule WHERE schedule_id = schedule.schedule_id)",
	}
	arguments := []any{cancelledAt}
	switch request.Scope {
	case task.ScheduleCancelScopeCurrentConversation:
		conversationID := strings.TrimSpace(request.ConversationID)
		if conversationID == "" {
			return task.ScheduleCancelResult{}, nil
		}
		conditions = append(conditions, "delivery_conversation_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, conversationID)
	case task.ScheduleCancelScopeScheduleIDs:
		condition, values := scheduleIDCondition(request.ScheduleIDs, len(arguments)+1)
		if condition == "" {
			return task.ScheduleCancelResult{}, nil
		}
		conditions = append(conditions, condition)
		arguments = append(arguments, values...)
		conditions = append(conditions, scheduleCancelAccessCondition(request, len(arguments)+1))
		if strings.TrimSpace(request.ConversationID) != "" {
			arguments = append(arguments, requesterPersonID, strings.TrimSpace(request.ConversationID))
		} else {
			arguments = append(arguments, requesterPersonID)
		}
	default:
		conditions = append(conditions, "creator_person_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, requesterPersonID)
	}
	query := `UPDATE schedule
SET expires_at = $1,
  next_run_at = NULL,
  lease_owner = '',
  leased_until = NULL,
  updated_at = $1
WHERE ` + strings.Join(conditions, " AND ") + `
RETURNING ` + scheduleReturningColumns()
	rows, errorValue := scheduleRepository.database.SQL.QueryContext(context.Background(), query, arguments...)
	if errorValue != nil {
		return task.ScheduleCancelResult{}, errorValue
	}
	defer rows.Close()
	schedules, errorValue := scanSchedules(rows)
	if errorValue != nil {
		return task.ScheduleCancelResult{}, errorValue
	}
	return task.ScheduleCancelResult{Schedules: schedules}, nil
}

func (scheduleRepository ScheduleRepository) SummarizeActiveSchedules(referenceTime time.Time) (task.ScheduleSummary, error) {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	var summary task.ScheduleSummary
	var earliestNextRunAt sql.NullTime
	var latestNextRunAt sql.NullTime
	errorValue := scheduleRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT
  count(*),
  count(*) FILTER (WHERE expires_at IS NULL AND max_run_count IS NULL),
  count(*) FILTER (WHERE interval_second IS NOT NULL),
  count(*) FILTER (WHERE cron_expression IS NOT NULL),
  count(*) FILTER (WHERE schedule_kind = 'once'),
  min(next_run_at),
  max(next_run_at)
FROM schedule
WHERE next_run_at IS NOT NULL
  AND (expires_at IS NULL OR expires_at > $1)`,
		referenceTime,
	).Scan(
		&summary.ActiveCount,
		&summary.UnboundedCount,
		&summary.IntervalCount,
		&summary.CronCount,
		&summary.OnceCount,
		&earliestNextRunAt,
		&latestNextRunAt,
	)
	if errorValue != nil {
		return task.ScheduleSummary{}, errorValue
	}
	summary.EarliestNextRunAt = nullableScheduleTime(earliestNextRunAt)
	summary.LatestNextRunAt = nullableScheduleTime(latestNextRunAt)
	summary.CheckedAt = referenceTime
	return summary, nil
}

func (scheduleRepository ScheduleRepository) ListSchedules(request task.ScheduleListRequest) (task.ScheduleListResult, error) {
	conditions, filterArguments := scheduleListFilter(request)
	page := scheduleListPage(request.Page)
	pageSize := scheduleListPageSize(request.PageSize)
	queryArguments := append([]any{}, filterArguments...)
	queryArguments = append(queryArguments, pageSize, (page-1)*pageSize)
	query := `SELECT ` + scheduleReturningColumns() + `
FROM schedule
WHERE ` + strings.Join(conditions, " AND ") + `
ORDER BY created_at DESC
LIMIT $` + strconv.Itoa(len(filterArguments)+1) + `
OFFSET $` + strconv.Itoa(len(filterArguments)+2)
	rows, errorValue := scheduleRepository.database.SQL.QueryContext(context.Background(), query, queryArguments...)
	if errorValue != nil {
		return task.ScheduleListResult{}, errorValue
	}
	defer rows.Close()
	schedules, errorValue := scanSchedules(rows)
	if errorValue != nil {
		return task.ScheduleListResult{}, errorValue
	}
	totalCount, errorValue := scheduleRepository.countSchedules(conditions, filterArguments)
	if errorValue != nil {
		return task.ScheduleListResult{}, errorValue
	}
	return task.ScheduleListResult{Schedules: schedules, TotalCount: totalCount, Page: page, PageSize: pageSize}, nil
}

func (scheduleRepository ScheduleRepository) RepairScheduleCreatorPersonID(request task.ScheduleCreatorRepairRequest) (task.ScheduleCreatorRepairResult, error) {
	result, errorValue := scheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE schedule
SET creator_person_id = $2,
  updated_at = now()
WHERE creator_person_id = $1`,
		strings.TrimSpace(request.FromCreatorPersonID),
		strings.TrimSpace(request.ToCreatorPersonID),
	)
	if errorValue != nil {
		return task.ScheduleCreatorRepairResult{}, errorValue
	}
	updatedCount, errorValue := result.RowsAffected()
	if errorValue != nil {
		return task.ScheduleCreatorRepairResult{}, errorValue
	}
	return task.ScheduleCreatorRepairResult{UpdatedCount: int(updatedCount)}, nil
}

func (scheduleRepository ScheduleRepository) CountEmptyScheduleTimeZone() (int, error) {
	row := scheduleRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM schedule WHERE btrim(time_zone) = ''`)
	var emptyCount int
	if errorValue := row.Scan(&emptyCount); errorValue != nil {
		return 0, errorValue
	}
	return emptyCount, nil
}

func (scheduleRepository ScheduleRepository) FillEmptyScheduleTimeZone(timeZone string) (int, error) {
	result, errorValue := scheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE schedule
SET time_zone = $1,
  updated_at = now()
WHERE btrim(time_zone) = ''`,
		strings.TrimSpace(timeZone),
	)
	if errorValue != nil {
		return 0, errorValue
	}
	filledCount, errorValue := result.RowsAffected()
	if errorValue != nil {
		return 0, errorValue
	}
	return int(filledCount), nil
}

func (scheduleRepository ScheduleRepository) countSchedules(conditions []string, arguments []any) (int, error) {
	query := `SELECT COUNT(*) FROM schedule WHERE ` + strings.Join(conditions, " AND ")
	row := scheduleRepository.database.SQL.QueryRowContext(context.Background(), query, arguments...)
	var count int
	errorValue := row.Scan(&count)
	return count, errorValue
}

func scheduleListFilter(request task.ScheduleListRequest) ([]string, []any) {
	referenceTime := request.ReferenceTime
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	conditions := []string{"TRUE"}
	arguments := []any{}
	if !request.IncludeExpired {
		arguments = append(arguments, referenceTime)
		conditions = append(conditions, "next_run_at IS NOT NULL", "(expires_at IS NULL OR expires_at > $1)")
	}
	if strings.TrimSpace(request.ConversationID) != "" {
		conditions = append(conditions, "delivery_conversation_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, strings.TrimSpace(request.ConversationID))
	}
	if strings.TrimSpace(request.CreatorPersonID) != "" {
		conditions = append(conditions, "creator_person_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, strings.TrimSpace(request.CreatorPersonID))
	}
	if request.UnboundedOnly {
		conditions = append(conditions, "expires_at IS NULL", "max_run_count IS NULL")
	}
	return conditions, arguments
}

func scheduleListPage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

func scheduleListPageSize(pageSize int) int {
	if pageSize < 1 {
		return 50
	}
	if pageSize > 200 {
		return 200
	}
	return pageSize
}

func scheduleCancelAccessCondition(request task.ScheduleCancelRequest, firstPlaceholderIndex int) string {
	if strings.TrimSpace(request.ConversationID) == "" {
		return "creator_person_id = $" + strconv.Itoa(firstPlaceholderIndex)
	}
	return "(creator_person_id = $" + strconv.Itoa(firstPlaceholderIndex) + " OR delivery_conversation_id = $" + strconv.Itoa(firstPlaceholderIndex+1) + ")"
}

func scheduleIDCondition(scheduleIDs []string, firstPlaceholderIndex int) (string, []any) {
	placeholders := []string{}
	values := []any{}
	seenValues := map[string]bool{}
	for _, scheduleID := range scheduleIDs {
		trimmedScheduleID := strings.TrimSpace(scheduleID)
		if trimmedScheduleID == "" || seenValues[trimmedScheduleID] {
			continue
		}
		seenValues[trimmedScheduleID] = true
		values = append(values, trimmedScheduleID)
		placeholders = append(placeholders, "$"+strconv.Itoa(firstPlaceholderIndex+len(values)-1))
	}
	if len(placeholders) == 0 {
		return "", nil
	}
	return "schedule_id IN (" + strings.Join(placeholders, ",") + ")", values
}

func scheduleRetryDelay(failureCount int) time.Duration {
	if failureCount <= 0 {
		return time.Minute
	}
	delay := time.Duration(failureCount) * 5 * time.Minute
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

type scheduleScanner interface {
	Scan(...any) error
}

func scanSchedule(scanner scheduleScanner) (task.Schedule, error) {
	var schedule task.Schedule
	var executionMode string
	var kind string
	var cronExpression sql.NullString
	var intervalSecond sql.NullInt64
	var maxRunCount sql.NullInt64
	var runAt sql.NullTime
	var nextRunAt sql.NullTime
	var lastRunAt sql.NullTime
	var leasedUntil sql.NullTime
	var nextAttemptAt sql.NullTime
	var expiresAt sql.NullTime
	errorValue := scanner.Scan(
		&schedule.ScheduleID,
		&schedule.CreatorPersonID,
		&schedule.Name,
		&schedule.Prompt,
		&schedule.AgentProfileName,
		&executionMode,
		&kind,
		&runAt,
		&intervalSecond,
		&cronExpression,
		&nextRunAt,
		&lastRunAt,
		&schedule.LastTaskRunID,
		&expiresAt,
		&schedule.CreatedAt,
		&schedule.UpdatedAt,
		&schedule.Platform,
		&schedule.ConversationID,
		&schedule.ReplyTargetID,
		&schedule.TimeZone,
		&schedule.LeaseOwner,
		&leasedUntil,
		&schedule.FailureCount,
		&schedule.LastError,
		&nextAttemptAt,
		&maxRunCount,
		&schedule.CompletedRunCount,
	)
	schedule.ExecutionMode = task.ScheduleExecutionMode(normalizedScheduleExecutionMode(task.ScheduleExecutionMode(executionMode)))
	schedule.Kind = task.ScheduleKind(kind)
	if intervalSecond.Valid {
		schedule.IntervalSecond = int(intervalSecond.Int64)
	}
	if maxRunCount.Valid {
		schedule.MaxRunCount = int(maxRunCount.Int64)
	}
	if cronExpression.Valid {
		schedule.CronExpression = cronExpression.String
	}
	schedule.RunAt = nullableScheduleTime(runAt)
	schedule.NextRunAt = nullableScheduleTime(nextRunAt)
	schedule.LastRunAt = nullableScheduleTime(lastRunAt)
	schedule.LeasedUntil = nullableScheduleTime(leasedUntil)
	schedule.NextAttemptAt = nullableScheduleTime(nextAttemptAt)
	if expiresAt.Valid {
		schedule.ExpiresAt = &expiresAt.Time
	}
	return schedule, errorValue
}

func normalizedScheduleExecutionMode(value task.ScheduleExecutionMode) string {
	switch value {
	case task.ScheduleExecutionModeMessage:
		return string(task.ScheduleExecutionModeMessage)
	default:
		return string(task.ScheduleExecutionModeAgent)
	}
}

func scanSchedules(rows *sql.Rows) ([]task.Schedule, error) {
	schedules := []task.Schedule{}
	for rows.Next() {
		schedule, errorValue := scanSchedule(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func scheduleReturningColumns() string {
	return `schedule_id, COALESCE(creator_person_id, ''), name, prompt, agent_profile_name,
  execution_mode, schedule_kind, run_at, interval_second, cron_expression, next_run_at,
  last_run_at, COALESCE(last_task_run_id, ''), expires_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone,
  lease_owner, leased_until, failure_count, last_error, next_attempt_at,
  max_run_count, completed_run_count`
}

func scheduleExpiresAt(schedule task.Schedule) any {
	if schedule.ExpiresAt == nil || schedule.ExpiresAt.IsZero() {
		return nil
	}
	return schedule.ExpiresAt.UTC()
}

func zeroAsNil(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func firstNonNilScheduleTime(pointerValue *time.Time, fallbackValue time.Time) *time.Time {
	if pointerValue != nil && !pointerValue.IsZero() {
		return pointerValue
	}
	if fallbackValue.IsZero() {
		return nil
	}
	return &fallbackValue
}

func nullableScheduleTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func firstNonEmptyPostgresString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ task.ScheduleRepository = ScheduleRepository{}
