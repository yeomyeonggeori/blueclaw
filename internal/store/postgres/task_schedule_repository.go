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

type TaskScheduleRepository struct {
	database Database
}

func NewTaskScheduleRepository(database Database) TaskScheduleRepository {
	return TaskScheduleRepository{database: database}
}

func (taskScheduleRepository TaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	now := time.Now().UTC()
	if taskSchedule.CreatedAt.IsZero() {
		taskSchedule.CreatedAt = now
	}
	if taskSchedule.UpdatedAt.IsZero() {
		taskSchedule.UpdatedAt = now
	}
	_, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_schedule (
  task_schedule_id, creator_person_id, name, prompt, execution_mode, agent_profile_name,
  schedule_kind, run_at, interval_second, cron_expression, next_run_at,
  last_run_at, last_task_run_id, expires_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone,
  lease_owner, leased_until, failure_count, last_error, next_attempt_at,
  max_run_count, completed_run_count
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
ON CONFLICT (task_schedule_id) DO UPDATE SET
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
  completed_run_count = EXCLUDED.completed_run_count`,
		taskSchedule.TaskScheduleID,
		emptyStringAsNil(taskSchedule.CreatorPersonID),
		taskSchedule.Name,
		taskSchedule.Prompt,
		normalizedTaskScheduleExecutionMode(taskSchedule.ExecutionMode),
		taskSchedule.AgentProfileName,
		string(taskSchedule.Kind),
		taskSchedule.RunAt,
		zeroAsNil(taskSchedule.IntervalSecond),
		emptyStringAsNil(taskSchedule.CronExpression),
		taskSchedule.NextRunAt,
		taskSchedule.LastRunAt,
		emptyStringAsNil(taskSchedule.LastTaskRunID),
		taskScheduleExpiresAt(taskSchedule),
		taskSchedule.CreatedAt,
		taskSchedule.UpdatedAt,
		taskSchedule.Platform,
		taskSchedule.ConversationID,
		taskSchedule.ReplyTargetID,
		taskSchedule.TimeZone,
		taskSchedule.LeaseOwner,
		taskSchedule.LeasedUntil,
		taskSchedule.FailureCount,
		taskSchedule.LastError,
		firstNonNilTaskScheduleTime(taskSchedule.NextAttemptAt, taskSchedule.CreatedAt),
		zeroAsNil(taskSchedule.MaxRunCount),
		taskSchedule.CompletedRunCount,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) UpdateTaskSchedule(request task.TaskScheduleUpdateRequest) (task.TaskScheduleUpdateResult, error) {
	taskScheduleID := strings.TrimSpace(request.TaskScheduleID)
	requesterPersonID := strings.TrimSpace(request.RequesterPersonID)
	if taskScheduleID == "" || requesterPersonID == "" {
		return task.TaskScheduleUpdateResult{}, nil
	}
	transaction, errorValue := taskScheduleRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return task.TaskScheduleUpdateResult{}, errorValue
	}
	row := transaction.QueryRowContext(context.Background(), `SELECT `+taskScheduleReturningColumns()+`
FROM task_schedule
WHERE task_schedule_id = $1
  AND creator_person_id = $2
  AND next_run_at IS NOT NULL
  AND (expires_at IS NULL OR expires_at > now())
FOR UPDATE`, taskScheduleID, requesterPersonID)
	taskSchedule, errorValue := scanTaskSchedule(row)
	if errors.Is(errorValue, sql.ErrNoRows) {
		_ = transaction.Rollback()
		return task.TaskScheduleUpdateResult{}, nil
	}
	if errorValue != nil {
		_ = transaction.Rollback()
		return task.TaskScheduleUpdateResult{}, errorValue
	}
	if request.UpdateTaskSchedule != nil {
		taskSchedule, errorValue = request.UpdateTaskSchedule(taskSchedule)
		if errorValue != nil {
			_ = transaction.Rollback()
			return task.TaskScheduleUpdateResult{}, errorValue
		}
	}
	if errorValue := updateTaskScheduleWithTransaction(transaction, taskSchedule); errorValue != nil {
		_ = transaction.Rollback()
		return task.TaskScheduleUpdateResult{}, errorValue
	}
	if errorValue := transaction.Commit(); errorValue != nil {
		return task.TaskScheduleUpdateResult{}, errorValue
	}
	return task.TaskScheduleUpdateResult{TaskSchedule: taskSchedule, IsFound: true}, nil
}

func (taskScheduleRepository TaskScheduleRepository) DeleteTaskSchedule(request task.TaskScheduleDeleteRequest) (task.TaskScheduleDeleteResult, error) {
	taskScheduleID := strings.TrimSpace(request.TaskScheduleID)
	requesterPersonID := strings.TrimSpace(request.RequesterPersonID)
	if taskScheduleID == "" || requesterPersonID == "" {
		return task.TaskScheduleDeleteResult{}, nil
	}
	row := taskScheduleRepository.database.SQL.QueryRowContext(context.Background(), `
DELETE FROM task_schedule
WHERE task_schedule_id = $1
  AND creator_person_id = $2
RETURNING `+taskScheduleReturningColumns(), taskScheduleID, requesterPersonID)
	taskSchedule, errorValue := scanTaskSchedule(row)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return task.TaskScheduleDeleteResult{}, nil
	}
	if errorValue != nil {
		return task.TaskScheduleDeleteResult{}, errorValue
	}
	return task.TaskScheduleDeleteResult{TaskSchedule: taskSchedule, IsFound: true}, nil
}

func (taskScheduleRepository TaskScheduleRepository) ClaimDueTaskSchedules(limit int, leaseDuration time.Duration, referenceTime time.Time, leaseOwner string) ([]task.TaskSchedule, error) {
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
	rows, errorValue := taskScheduleRepository.database.SQL.QueryContext(context.Background(), `
WITH claim AS (
  SELECT task_schedule_id FROM task_schedule
  WHERE next_run_at IS NOT NULL
    AND next_run_at <= $1
    AND next_attempt_at <= $1
    AND (expires_at IS NULL OR expires_at > $1)
    AND (leased_until IS NULL OR leased_until <= $1 OR lease_owner = $3)
  ORDER BY next_run_at ASC
  LIMIT $2
  FOR UPDATE SKIP LOCKED
)
UPDATE task_schedule
SET lease_owner = $3,
  leased_until = $1 + ($4 * interval '1 second'),
  updated_at = $1
WHERE task_schedule_id IN (SELECT task_schedule_id FROM claim)
RETURNING `+taskScheduleReturningColumns(),
		referenceTime,
		limit,
		leaseOwner,
		leaseSecond,
	)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskSchedules(rows)
}

func (taskScheduleRepository TaskScheduleRepository) MarkTaskScheduleSucceeded(taskSchedule task.TaskSchedule) error {
	now := time.Now().UTC()
	_, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_schedule
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
WHERE task_schedule_id = $5`,
		now,
		taskSchedule.NextRunAt,
		taskSchedule.LastRunAt,
		emptyStringAsNil(taskSchedule.LastTaskRunID),
		taskSchedule.TaskScheduleID,
		taskSchedule.CompletedRunCount,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) MarkTaskScheduleSucceededAndEnqueueDelivery(taskSchedule task.TaskSchedule, taskRunID string, deliveryDeduplicationKey string, reply connectors.OutboundReply) (string, error) {
	deliveryDeduplicationKey = strings.TrimSpace(deliveryDeduplicationKey)
	if deliveryDeduplicationKey == "" {
		return "", errors.New("scheduled task delivery deduplication key is required")
	}
	now := time.Now().UTC()
	transaction, errorValue := taskScheduleRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return "", errorValue
	}
	defer transaction.Rollback()
	conversationID, errorValue := ensureConversationWithTransaction(transaction, taskSchedule.Platform, taskSchedule.ConversationID, now)
	if errorValue != nil {
		return "", errorValue
	}
	if errorValue := enqueueScheduledConnectorReplyWithTransaction(transaction, taskSchedule, taskRunID, deliveryDeduplicationKey, conversationID, reply, now); errorValue != nil {
		return "", errorValue
	}
	if errorValue := markTaskScheduleSucceededWithTransaction(transaction, taskSchedule, now); errorValue != nil {
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

func enqueueScheduledConnectorReplyWithTransaction(transaction *sql.Tx, taskSchedule task.TaskSchedule, taskRunID string, deliveryDeduplicationKey string, conversationID string, reply connectors.OutboundReply, now time.Time) error {
	reply = scheduledConnectorReply(taskRunID, deliveryDeduplicationKey, reply)
	replyTarget := connectors.ReplyTarget{
		ConversationID: taskSchedule.ConversationID,
		ReplyTargetID:  taskSchedule.ReplyTargetID,
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
	contentHash := sha256.Sum256([]byte(taskSchedule.Prompt))
	_, errorValue = transaction.ExecContext(context.Background(), `
INSERT INTO raw_event (
  raw_event_id, platform, conversation_id, external_message_id, event_type,
  content_ciphertext, encryption_key_version, content_sha256, security_level_rank,
  required_classes, occurred_at, ingested_at, expires_at,
  reply_target_id, visible_context_ciphertext, visible_context_sha256, has_more_before, history_cursor
) VALUES ($1,$2,$3,$1,'scheduled_task',$4,1,$5,0,'{}',$6,$6,$7,$8,$9,$10,false,NULL)
ON CONFLICT (raw_event_id) DO NOTHING`,
		deliveryDeduplicationKey,
		taskSchedule.Platform,
		conversationID,
		[]byte(taskSchedule.Prompt),
		contentHash[:],
		now,
		now.AddDate(0, 0, 60),
		taskSchedule.ReplyTargetID,
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
		taskSchedule.Platform,
		taskSchedule.ReplyTargetID,
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

func markTaskScheduleSucceededWithTransaction(transaction *sql.Tx, taskSchedule task.TaskSchedule, now time.Time) error {
	_, errorValue := transaction.ExecContext(context.Background(), `
UPDATE task_schedule
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
WHERE task_schedule_id = $5`,
		now,
		taskSchedule.NextRunAt,
		taskSchedule.LastRunAt,
		emptyStringAsNil(taskSchedule.LastTaskRunID),
		taskSchedule.TaskScheduleID,
		taskSchedule.CompletedRunCount,
	)
	return errorValue
}

func updateTaskScheduleWithTransaction(transaction *sql.Tx, taskSchedule task.TaskSchedule) error {
	_, errorValue := transaction.ExecContext(context.Background(), `
UPDATE task_schedule
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
WHERE task_schedule_id = $1`,
		taskSchedule.TaskScheduleID,
		taskSchedule.Name,
		taskSchedule.Prompt,
		normalizedTaskScheduleExecutionMode(taskSchedule.ExecutionMode),
		taskSchedule.AgentProfileName,
		string(taskSchedule.Kind),
		taskSchedule.RunAt,
		zeroAsNil(taskSchedule.IntervalSecond),
		emptyStringAsNil(taskSchedule.CronExpression),
		taskSchedule.NextRunAt,
		taskSchedule.LastRunAt,
		emptyStringAsNil(taskSchedule.LastTaskRunID),
		taskScheduleExpiresAt(taskSchedule),
		taskSchedule.UpdatedAt,
		taskSchedule.Platform,
		taskSchedule.ConversationID,
		taskSchedule.ReplyTargetID,
		taskSchedule.TimeZone,
		taskSchedule.LeaseOwner,
		taskSchedule.LeasedUntil,
		taskSchedule.FailureCount,
		taskSchedule.LastError,
		firstNonNilTaskScheduleTime(taskSchedule.NextAttemptAt, taskSchedule.UpdatedAt),
		zeroAsNil(taskSchedule.MaxRunCount),
		taskSchedule.CompletedRunCount,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) MarkTaskScheduleFailed(taskSchedule task.TaskSchedule, errorMessage string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	nextAttemptAt := referenceTime.Add(taskScheduleRetryDelay(taskSchedule.FailureCount + 1))
	_, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_schedule
SET lease_owner = '',
  leased_until = NULL,
  failure_count = failure_count + 1,
  last_error = $1,
  next_attempt_at = $2,
  updated_at = $3
WHERE task_schedule_id = $4`,
		errorMessage,
		nextAttemptAt,
		referenceTime,
		taskSchedule.TaskScheduleID,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) ExpireTaskSchedule(taskSchedule task.TaskSchedule, errorMessage string, referenceTime time.Time) error {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	_, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_schedule
SET expires_at = $1,
  next_run_at = NULL,
  lease_owner = '',
  leased_until = NULL,
  failure_count = failure_count + 1,
  last_error = $2,
  next_attempt_at = $1,
  updated_at = $1
WHERE task_schedule_id = $3`,
		referenceTime,
		errorMessage,
		taskSchedule.TaskScheduleID,
	)
	return errorValue
}

func (taskScheduleRepository TaskScheduleRepository) CancelTaskSchedules(request task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	cancelledAt := request.CancelledAt
	if cancelledAt.IsZero() {
		cancelledAt = time.Now().UTC()
	}
	requesterPersonID := strings.TrimSpace(request.RequesterPersonID)
	if requesterPersonID == "" {
		return task.TaskScheduleCancelResult{}, nil
	}
	conditions := []string{
		"next_run_at IS NOT NULL",
		"(expires_at IS NULL OR expires_at > $1)",
	}
	arguments := []any{cancelledAt}
	switch request.Scope {
	case task.TaskScheduleCancelScopeCurrentConversation:
		conversationID := strings.TrimSpace(request.ConversationID)
		if conversationID == "" {
			return task.TaskScheduleCancelResult{}, nil
		}
		conditions = append(conditions, "delivery_conversation_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, conversationID)
	case task.TaskScheduleCancelScopeScheduleIDs:
		condition, values := taskScheduleIDCondition(request.TaskScheduleIDs, len(arguments)+1)
		if condition == "" {
			return task.TaskScheduleCancelResult{}, nil
		}
		conditions = append(conditions, condition)
		arguments = append(arguments, values...)
		conditions = append(conditions, taskScheduleCancelAccessCondition(request, len(arguments)+1))
		if strings.TrimSpace(request.ConversationID) != "" {
			arguments = append(arguments, requesterPersonID, strings.TrimSpace(request.ConversationID))
		} else {
			arguments = append(arguments, requesterPersonID)
		}
	default:
		conditions = append(conditions, "creator_person_id = $"+strconv.Itoa(len(arguments)+1))
		arguments = append(arguments, requesterPersonID)
	}
	query := `UPDATE task_schedule
SET expires_at = $1,
  next_run_at = NULL,
  lease_owner = '',
  leased_until = NULL,
  updated_at = $1
WHERE ` + strings.Join(conditions, " AND ") + `
RETURNING ` + taskScheduleReturningColumns()
	rows, errorValue := taskScheduleRepository.database.SQL.QueryContext(context.Background(), query, arguments...)
	if errorValue != nil {
		return task.TaskScheduleCancelResult{}, errorValue
	}
	defer rows.Close()
	taskSchedules, errorValue := scanTaskSchedules(rows)
	if errorValue != nil {
		return task.TaskScheduleCancelResult{}, errorValue
	}
	return task.TaskScheduleCancelResult{TaskSchedules: taskSchedules}, nil
}

func (taskScheduleRepository TaskScheduleRepository) SummarizeActiveTaskSchedules(referenceTime time.Time) (task.TaskScheduleSummary, error) {
	if referenceTime.IsZero() {
		referenceTime = time.Now().UTC()
	}
	var summary task.TaskScheduleSummary
	var earliestNextRunAt sql.NullTime
	var latestNextRunAt sql.NullTime
	errorValue := taskScheduleRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT
  count(*),
  count(*) FILTER (WHERE expires_at IS NULL AND max_run_count IS NULL),
  count(*) FILTER (WHERE interval_second IS NOT NULL),
  count(*) FILTER (WHERE cron_expression IS NOT NULL),
  count(*) FILTER (WHERE schedule_kind = 'once'),
  min(next_run_at),
  max(next_run_at)
FROM task_schedule
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
		return task.TaskScheduleSummary{}, errorValue
	}
	summary.EarliestNextRunAt = nullableTaskScheduleTime(earliestNextRunAt)
	summary.LatestNextRunAt = nullableTaskScheduleTime(latestNextRunAt)
	summary.CheckedAt = referenceTime
	return summary, nil
}

func (taskScheduleRepository TaskScheduleRepository) ListTaskSchedules(request task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	conditions, filterArguments := taskScheduleListFilter(request)
	page := taskScheduleListPage(request.Page)
	pageSize := taskScheduleListPageSize(request.PageSize)
	queryArguments := append([]any{}, filterArguments...)
	queryArguments = append(queryArguments, pageSize, (page-1)*pageSize)
	query := `SELECT ` + taskScheduleReturningColumns() + `
FROM task_schedule
WHERE ` + strings.Join(conditions, " AND ") + `
ORDER BY created_at DESC
LIMIT $` + strconv.Itoa(len(filterArguments)+1) + `
OFFSET $` + strconv.Itoa(len(filterArguments)+2)
	rows, errorValue := taskScheduleRepository.database.SQL.QueryContext(context.Background(), query, queryArguments...)
	if errorValue != nil {
		return task.TaskScheduleListResult{}, errorValue
	}
	defer rows.Close()
	taskSchedules, errorValue := scanTaskSchedules(rows)
	if errorValue != nil {
		return task.TaskScheduleListResult{}, errorValue
	}
	totalCount, errorValue := taskScheduleRepository.countTaskSchedules(conditions, filterArguments)
	if errorValue != nil {
		return task.TaskScheduleListResult{}, errorValue
	}
	return task.TaskScheduleListResult{TaskSchedules: taskSchedules, TotalCount: totalCount, Page: page, PageSize: pageSize}, nil
}

func (taskScheduleRepository TaskScheduleRepository) RepairTaskScheduleCreatorPersonID(request task.TaskScheduleCreatorRepairRequest) (task.TaskScheduleCreatorRepairResult, error) {
	result, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_schedule
SET creator_person_id = $2,
  updated_at = now()
WHERE creator_person_id = $1`,
		strings.TrimSpace(request.FromCreatorPersonID),
		strings.TrimSpace(request.ToCreatorPersonID),
	)
	if errorValue != nil {
		return task.TaskScheduleCreatorRepairResult{}, errorValue
	}
	updatedCount, errorValue := result.RowsAffected()
	if errorValue != nil {
		return task.TaskScheduleCreatorRepairResult{}, errorValue
	}
	return task.TaskScheduleCreatorRepairResult{UpdatedCount: int(updatedCount)}, nil
}

func (taskScheduleRepository TaskScheduleRepository) CountEmptyTaskScheduleTimeZone() (int, error) {
	row := taskScheduleRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM task_schedule WHERE btrim(time_zone) = ''`)
	var emptyCount int
	if errorValue := row.Scan(&emptyCount); errorValue != nil {
		return 0, errorValue
	}
	return emptyCount, nil
}

func (taskScheduleRepository TaskScheduleRepository) FillEmptyTaskScheduleTimeZone(timeZone string) (int, error) {
	result, errorValue := taskScheduleRepository.database.SQL.ExecContext(context.Background(), `
UPDATE task_schedule
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

func (taskScheduleRepository TaskScheduleRepository) countTaskSchedules(conditions []string, arguments []any) (int, error) {
	query := `SELECT COUNT(*) FROM task_schedule WHERE ` + strings.Join(conditions, " AND ")
	row := taskScheduleRepository.database.SQL.QueryRowContext(context.Background(), query, arguments...)
	var count int
	errorValue := row.Scan(&count)
	return count, errorValue
}

func taskScheduleListFilter(request task.TaskScheduleListRequest) ([]string, []any) {
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

func taskScheduleListPage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

func taskScheduleListPageSize(pageSize int) int {
	if pageSize < 1 {
		return 50
	}
	if pageSize > 200 {
		return 200
	}
	return pageSize
}

func taskScheduleCancelAccessCondition(request task.TaskScheduleCancelRequest, firstPlaceholderIndex int) string {
	if strings.TrimSpace(request.ConversationID) == "" {
		return "creator_person_id = $" + strconv.Itoa(firstPlaceholderIndex)
	}
	return "(creator_person_id = $" + strconv.Itoa(firstPlaceholderIndex) + " OR delivery_conversation_id = $" + strconv.Itoa(firstPlaceholderIndex+1) + ")"
}

func taskScheduleIDCondition(taskScheduleIDs []string, firstPlaceholderIndex int) (string, []any) {
	placeholders := []string{}
	values := []any{}
	seenValues := map[string]bool{}
	for _, taskScheduleID := range taskScheduleIDs {
		trimmedTaskScheduleID := strings.TrimSpace(taskScheduleID)
		if trimmedTaskScheduleID == "" || seenValues[trimmedTaskScheduleID] {
			continue
		}
		seenValues[trimmedTaskScheduleID] = true
		values = append(values, trimmedTaskScheduleID)
		placeholders = append(placeholders, "$"+strconv.Itoa(firstPlaceholderIndex+len(values)-1))
	}
	if len(placeholders) == 0 {
		return "", nil
	}
	return "task_schedule_id IN (" + strings.Join(placeholders, ",") + ")", values
}

func taskScheduleRetryDelay(failureCount int) time.Duration {
	if failureCount <= 0 {
		return time.Minute
	}
	delay := time.Duration(failureCount) * 5 * time.Minute
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

type taskScheduleScanner interface {
	Scan(...any) error
}

func scanTaskSchedule(scanner taskScheduleScanner) (task.TaskSchedule, error) {
	var taskSchedule task.TaskSchedule
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
		&taskSchedule.TaskScheduleID,
		&taskSchedule.CreatorPersonID,
		&taskSchedule.Name,
		&taskSchedule.Prompt,
		&taskSchedule.AgentProfileName,
		&executionMode,
		&kind,
		&runAt,
		&intervalSecond,
		&cronExpression,
		&nextRunAt,
		&lastRunAt,
		&taskSchedule.LastTaskRunID,
		&expiresAt,
		&taskSchedule.CreatedAt,
		&taskSchedule.UpdatedAt,
		&taskSchedule.Platform,
		&taskSchedule.ConversationID,
		&taskSchedule.ReplyTargetID,
		&taskSchedule.TimeZone,
		&taskSchedule.LeaseOwner,
		&leasedUntil,
		&taskSchedule.FailureCount,
		&taskSchedule.LastError,
		&nextAttemptAt,
		&maxRunCount,
		&taskSchedule.CompletedRunCount,
	)
	taskSchedule.ExecutionMode = task.TaskScheduleExecutionMode(normalizedTaskScheduleExecutionMode(task.TaskScheduleExecutionMode(executionMode)))
	taskSchedule.Kind = task.TaskScheduleKind(kind)
	if intervalSecond.Valid {
		taskSchedule.IntervalSecond = int(intervalSecond.Int64)
	}
	if maxRunCount.Valid {
		taskSchedule.MaxRunCount = int(maxRunCount.Int64)
	}
	if cronExpression.Valid {
		taskSchedule.CronExpression = cronExpression.String
	}
	taskSchedule.RunAt = nullableTaskScheduleTime(runAt)
	taskSchedule.NextRunAt = nullableTaskScheduleTime(nextRunAt)
	taskSchedule.LastRunAt = nullableTaskScheduleTime(lastRunAt)
	taskSchedule.LeasedUntil = nullableTaskScheduleTime(leasedUntil)
	taskSchedule.NextAttemptAt = nullableTaskScheduleTime(nextAttemptAt)
	if expiresAt.Valid {
		taskSchedule.ExpiresAt = &expiresAt.Time
	}
	return taskSchedule, errorValue
}

func normalizedTaskScheduleExecutionMode(value task.TaskScheduleExecutionMode) string {
	switch value {
	case task.TaskScheduleExecutionModeMessage:
		return string(task.TaskScheduleExecutionModeMessage)
	default:
		return string(task.TaskScheduleExecutionModeAgent)
	}
}

func scanTaskSchedules(rows *sql.Rows) ([]task.TaskSchedule, error) {
	taskSchedules := []task.TaskSchedule{}
	for rows.Next() {
		taskSchedule, errorValue := scanTaskSchedule(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		taskSchedules = append(taskSchedules, taskSchedule)
	}
	return taskSchedules, rows.Err()
}

func taskScheduleReturningColumns() string {
	return `task_schedule_id, COALESCE(creator_person_id, ''), name, prompt, agent_profile_name,
  execution_mode, schedule_kind, run_at, interval_second, cron_expression, next_run_at,
  last_run_at, COALESCE(last_task_run_id, ''), expires_at, created_at, updated_at,
  platform, delivery_conversation_id, reply_target_id, time_zone,
  lease_owner, leased_until, failure_count, last_error, next_attempt_at,
  max_run_count, completed_run_count`
}

func taskScheduleExpiresAt(taskSchedule task.TaskSchedule) any {
	if taskSchedule.ExpiresAt == nil || taskSchedule.ExpiresAt.IsZero() {
		return nil
	}
	return taskSchedule.ExpiresAt.UTC()
}

func zeroAsNil(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func firstNonNilTaskScheduleTime(pointerValue *time.Time, fallbackValue time.Time) *time.Time {
	if pointerValue != nil && !pointerValue.IsZero() {
		return pointerValue
	}
	if fallbackValue.IsZero() {
		return nil
	}
	return &fallbackValue
}

func nullableTaskScheduleTime(value sql.NullTime) *time.Time {
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

var _ task.TaskScheduleRepository = TaskScheduleRepository{}
