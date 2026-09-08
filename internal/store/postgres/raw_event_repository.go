package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type RawEventRepository struct {
	database Database
}

func NewRawEventRepository(database Database) RawEventRepository {
	return RawEventRepository{database: database}
}

func (rawEventRepository RawEventRepository) ListConnectorEventDiagnostics(ctx context.Context, filter connectors.EventDiagnosticFilter) ([]connectors.EventDiagnostic, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	conditions := []string{"connector_event_json != '{}'::jsonb"}
	arguments := []any{}
	appendCondition := func(column string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		arguments = append(arguments, value)
		conditions = append(conditions, column+" = $"+strconv.Itoa(len(arguments)))
	}
	appendCondition("platform", filter.Platform)
	appendCondition("conversation_id", filter.ConversationID)
	appendCondition("external_message_id", filter.MessageID)
	arguments = append(arguments, limit)

	rows, errorValue := rawEventRepository.database.SQL.QueryContext(ctx, `
SELECT raw_event_id, platform, conversation_id, external_message_id, connector_status,
  connector_attempt_count, connector_error, connector_result_json, ingested_at,
  connector_started_at, connector_completed_at
FROM raw_event
WHERE `+strings.Join(conditions, " AND ")+`
ORDER BY ingested_at DESC
LIMIT $`+strconv.Itoa(len(arguments)), arguments...)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()

	diagnostics := []connectors.EventDiagnostic{}
	for rows.Next() {
		diagnostic, scanError := scanConnectorEventDiagnostic(rows)
		if scanError != nil {
			return nil, scanError
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics, rows.Err()
}

func scanConnectorEventDiagnostic(rows *sql.Rows) (connectors.EventDiagnostic, error) {
	var diagnostic connectors.EventDiagnostic
	var connectorError sql.NullString
	var result []byte
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	errorValue := rows.Scan(
		&diagnostic.RawEventID,
		&diagnostic.Platform,
		&diagnostic.ConversationID,
		&diagnostic.ExternalMessageID,
		&diagnostic.ConnectorStatus,
		&diagnostic.AttemptCount,
		&connectorError,
		&result,
		&diagnostic.IngestedAt,
		&startedAt,
		&completedAt,
	)
	if errorValue != nil {
		return connectors.EventDiagnostic{}, errorValue
	}
	diagnostic.ConnectorError = strings.TrimSpace(connectorError.String)
	diagnostic.Result = json.RawMessage(result)
	if startedAt.Valid {
		diagnostic.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		diagnostic.CompletedAt = &completedAt.Time
	}
	return diagnostic, nil
}

func (rawEventRepository RawEventRepository) InsertRawEvent(rawEventID string) error {
	_, errorValue := rawEventRepository.database.SQL.ExecContext(context.Background(), "INSERT INTO raw_event (raw_event_id) VALUES ($1)", rawEventID)
	return errorValue
}

func (rawEventRepository RawEventRepository) TryInsertConnectorEvent(event connectors.PlatformInboundEvent) (bool, connectors.ConnectorRuntimeResult, error) {
	conversationRepository := NewConversationRepository(rawEventRepository.database)
	conversationID, errorValue := conversationRepository.EnsureConversation(event.Platform, event.ConversationID)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}

	result := connectors.ConnectorRuntimeResult{}
	contentHash := sha256.Sum256([]byte(event.Prompt))
	rawEventID := event.DedupeKey()
	execResult, errorValue := rawEventRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO raw_event (
  raw_event_id, platform, conversation_id, external_message_id, event_type,
  content_ciphertext, encryption_key_version, content_sha256, security_level_rank,
  required_classes, occurred_at, ingested_at, expires_at,
  reply_target_id, visible_context_ciphertext, visible_context_sha256, has_more_before, history_cursor
) VALUES ($1,$2,$3,$4,'message',$5,1,$6,0,'{}',$7,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (platform, conversation_id, external_message_id) DO NOTHING`,
		rawEventID,
		event.Platform,
		conversationID,
		event.ExternalEventID(),
		[]byte(event.Prompt),
		contentHash[:],
		time.Now().UTC(),
		time.Now().UTC().AddDate(0, 0, 60),
		event.ReplyTargetID,
		mustJSON(event.Context),
		hashJSON(event.Context),
		event.Context.HasMoreBefore,
		emptyStringAsNil(event.Context.HistoryCursor),
	)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}
	affectedRows, errorValue := execResult.RowsAffected()
	if errorValue == nil && affectedRows == 1 {
		return false, result, nil
	}

	existingResult, fetchError := rawEventRepository.GetConnectorResult(event.Platform, conversationID, event.ExternalEventID())
	if fetchError != nil {
		return true, connectors.ConnectorRuntimeResult{}, fetchError
	}
	return true, existingResult, nil
}

func (rawEventRepository RawEventRepository) TryEnqueueConnectorEvent(event connectors.PlatformInboundEvent) (bool, connectors.ConnectorRuntimeResult, error) {
	conversationID, errorValue := NewConversationRepository(rawEventRepository.database).EnsureConversation(event.Platform, event.ConversationID)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}

	eventDocument, errorValue := marshalConnectorEvent(event)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}
	contentHash := sha256.Sum256([]byte(event.Prompt))
	rawEventID := event.DedupeKey()
	transaction, errorValue := rawEventRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}
	defer transaction.Rollback()
	execResult, errorValue := transaction.ExecContext(context.Background(), `
INSERT INTO raw_event (
  raw_event_id, platform, conversation_id, external_message_id, event_type,
  content_ciphertext, encryption_key_version, content_sha256, security_level_rank,
  required_classes, occurred_at, ingested_at, expires_at,
  reply_target_id, visible_context_ciphertext, visible_context_sha256, has_more_before, history_cursor,
  connector_event_json, connector_status, connector_next_attempt_at
) VALUES ($1,$2,$3,$4,'message',$5,1,$6,0,'{}',$7,$7,$8,$9,$10,$11,$12,$13,$14,'pending',$7)
ON CONFLICT (platform, conversation_id, external_message_id) DO NOTHING`,
		rawEventID,
		event.Platform,
		conversationID,
		event.ExternalEventID(),
		[]byte(event.Prompt),
		contentHash[:],
		time.Now().UTC(),
		time.Now().UTC().AddDate(0, 0, 60),
		event.ReplyTargetID,
		mustJSON(event.Context),
		hashJSON(event.Context),
		event.Context.HasMoreBefore,
		emptyStringAsNil(event.Context.HistoryCursor),
		eventDocument,
	)
	if errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}
	affectedRows, errorValue := execResult.RowsAffected()
	if errorValue == nil && affectedRows == 1 {
		if errorValue := suppressSupersededConnectorEvents(transaction, event.PreviousMessages); errorValue != nil {
			return false, connectors.ConnectorRuntimeResult{}, errorValue
		}
		if errorValue := transaction.Commit(); errorValue != nil {
			return false, connectors.ConnectorRuntimeResult{}, errorValue
		}
		return false, connectors.ConnectorRuntimeResult{}, nil
	}
	if errorValue := transaction.Commit(); errorValue != nil {
		return false, connectors.ConnectorRuntimeResult{}, errorValue
	}

	result, errorValue := rawEventRepository.GetConnectorResultByRawEventID(event)
	return true, result, errorValue
}

func (rawEventRepository RawEventRepository) ClaimPendingConnectorEvents(limit int, leaseDuration time.Duration) ([]connectors.QueuedConnectorEvent, error) {
	if limit <= 0 {
		limit = 1
	}
	now := time.Now().UTC()
	staleStartedAt := now.Add(-leaseDuration)
	rows, errorValue := rawEventRepository.database.SQL.QueryContext(context.Background(), `
WITH claim AS (
  SELECT raw_event_id FROM raw_event
  WHERE connector_event_json != '{}'::jsonb
    AND (
      (connector_status = 'pending' AND connector_next_attempt_at <= $1)
      OR (connector_status = 'running' AND connector_started_at <= $2)
    )
  ORDER BY ingested_at ASC
  LIMIT $3
  FOR UPDATE SKIP LOCKED
)
UPDATE raw_event
SET connector_status = 'running',
  connector_started_at = $1,
  connector_attempt_count = connector_attempt_count + 1,
  connector_error = ''
WHERE raw_event_id IN (SELECT raw_event_id FROM claim)
RETURNING connector_event_json, connector_attempt_count`,
		now,
		staleStartedAt,
		limit,
	)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()

	queuedEvents := []connectors.QueuedConnectorEvent{}
	for rows.Next() {
		var eventDocument []byte
		var attemptCount int
		if errorValue := rows.Scan(&eventDocument, &attemptCount); errorValue != nil {
			return nil, errorValue
		}
		event, errorValue := unmarshalConnectorEvent(eventDocument)
		if errorValue != nil {
			return nil, errorValue
		}
		queuedEvents = append(queuedEvents, connectors.QueuedConnectorEvent{Event: event, AttemptCount: attemptCount})
	}
	return queuedEvents, rows.Err()
}

func (rawEventRepository RawEventRepository) MarkConnectorEventSucceeded(event connectors.PlatformInboundEvent, result connectors.ConnectorRuntimeResult) error {
	resultDocument, errorValue := json.Marshal(result)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = rawEventRepository.database.SQL.ExecContext(context.Background(), `
UPDATE raw_event
SET connector_status = 'succeeded',
  connector_completed_at = $1,
  connector_result_json = $2,
  connector_error = ''
WHERE raw_event_id = $3
  AND connector_result_json->>'reason' IS DISTINCT FROM $4`,
		time.Now().UTC(),
		resultDocument,
		event.DedupeKey(),
		connectors.SupersededRequestReason,
	)
	return errorValue
}

func (rawEventRepository RawEventRepository) MarkConnectorEventFailed(queuedEvent connectors.QueuedConnectorEvent, errorValue error, nextAttemptAt time.Time) error {
	status := "failed"
	if !nextAttemptAt.IsZero() {
		status = "pending"
	} else {
		nextAttemptAt = time.Now().UTC()
	}
	_, updateError := rawEventRepository.database.SQL.ExecContext(context.Background(), `
UPDATE raw_event
SET connector_status = $1,
  connector_next_attempt_at = $2,
  connector_completed_at = $3,
  connector_error = $4
WHERE raw_event_id = $5
  AND connector_result_json->>'reason' IS DISTINCT FROM $6`,
		status,
		nextAttemptAt,
		time.Now().UTC(),
		errorValue.Error(),
		queuedEvent.Event.DedupeKey(),
		connectors.SupersededRequestReason,
	)
	return updateError
}

func (rawEventRepository RawEventRepository) SaveConnectorResult(event connectors.PlatformInboundEvent, result connectors.ConnectorRuntimeResult) error {
	conversationID := event.Platform + ":" + event.ConversationID
	resultDocument, errorValue := json.Marshal(result)
	if errorValue != nil {
		return errorValue
	}
	_, errorValue = rawEventRepository.database.SQL.ExecContext(context.Background(), `
UPDATE raw_event SET connector_result_json = $1
WHERE platform = $2 AND conversation_id = $3 AND external_message_id = $4`,
		resultDocument,
		event.Platform,
		conversationID,
		event.ExternalEventID(),
	)
	return errorValue
}

func (rawEventRepository RawEventRepository) GetConnectorResultByRawEventID(event connectors.PlatformInboundEvent) (connectors.ConnectorRuntimeResult, error) {
	var document []byte
	var status string
	errorValue := rawEventRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT connector_result_json, connector_status
FROM raw_event
WHERE raw_event_id = $1`, event.DedupeKey()).Scan(&document, &status)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return connectors.ConnectorRuntimeResult{}, errorValue
	}
	if errorValue != nil {
		return connectors.ConnectorRuntimeResult{}, errorValue
	}
	var result connectors.ConnectorRuntimeResult
	if errorValue := json.Unmarshal(document, &result); errorValue != nil {
		return connectors.ConnectorRuntimeResult{}, errorValue
	}
	if !result.Handled {
		result = connectors.ConnectorRuntimeResult{Handled: true, Platform: event.Platform, Reason: connectorResultReason(status)}
	}
	return result, nil
}

func (rawEventRepository RawEventRepository) GetConnectorResult(platform string, conversationID string, messageID string) (connectors.ConnectorRuntimeResult, error) {
	var document []byte
	errorValue := rawEventRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT connector_result_json FROM raw_event
WHERE platform = $1 AND conversation_id = $2 AND external_message_id = $3`,
		platform,
		conversationID,
		messageID,
	).Scan(&document)
	if errorValue != nil {
		return connectors.ConnectorRuntimeResult{}, errorValue
	}
	var result connectors.ConnectorRuntimeResult
	errorValue = json.Unmarshal(document, &result)
	return result, errorValue
}

func (rawEventRepository RawEventRepository) EnqueueConnectorReply(event connectors.PlatformInboundEvent, replyTarget connectors.ReplyTarget, reply connectors.OutboundReply) (string, error) {
	rawEventID := event.DedupeKey()
	outboxID := connectorReplyOutboxID(rawEventID, reply)
	reply.RawEventID = rawEventID
	reply.OutboxID = outboxID
	replyTargetDocument, errorValue := json.Marshal(replyTarget)
	if errorValue != nil {
		return "", errorValue
	}
	replyDocument, errorValue := json.Marshal(reply)
	if errorValue != nil {
		return "", errorValue
	}
	conversationID, errorValue := NewConversationRepository(rawEventRepository.database).EnsureConversation(event.Platform, event.ConversationID)
	if errorValue != nil {
		return "", errorValue
	}
	transaction, errorValue := rawEventRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return "", errorValue
	}
	defer transaction.Rollback()
	if errorValue := ensureSyntheticRawEvent(transaction, event, rawEventID, conversationID); errorValue != nil {
		return "", errorValue
	}
	var supersededReason sql.NullString
	if errorValue := transaction.QueryRowContext(context.Background(), `
SELECT connector_result_json->>'reason'
FROM raw_event
WHERE raw_event_id = $1
FOR UPDATE`, rawEventID).Scan(&supersededReason); errorValue != nil {
		return "", errorValue
	}
	if supersededReason.Valid && supersededReason.String == connectors.SupersededRequestReason {
		if errorValue := transaction.Commit(); errorValue != nil {
			return "", errorValue
		}
		return "", nil
	}
	_, errorValue = transaction.ExecContext(context.Background(), `
INSERT INTO connector_outbox (
  outbox_id, raw_event_id, platform, reply_target_id, reply_target_json, reply_json
) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (outbox_id) DO NOTHING`,
		outboxID,
		rawEventID,
		event.Platform,
		replyTarget.ReplyTargetID,
		replyTargetDocument,
		replyDocument,
	)
	if errorValue != nil {
		return "", errorValue
	}
	if errorValue = transaction.Commit(); errorValue != nil {
		return "", errorValue
	}
	return outboxID, nil
}

func ensureSyntheticRawEvent(transaction *sql.Tx, event connectors.PlatformInboundEvent, rawEventID string, conversationID string) error {
	externalMessageID := event.ExternalEventID()
	if externalMessageID == "" {
		externalMessageID = rawEventID
	}
	contentHash := sha256.Sum256([]byte(event.Prompt))
	now := time.Now().UTC()
	_, errorValue := transaction.ExecContext(context.Background(), `
INSERT INTO raw_event (
  raw_event_id, platform, conversation_id, external_message_id, event_type,
  content_ciphertext, encryption_key_version, content_sha256, security_level_rank,
  required_classes, occurred_at, ingested_at, expires_at,
  reply_target_id, visible_context_ciphertext, visible_context_sha256, has_more_before, history_cursor
) VALUES ($1,$2,$3,$4,'message',$5,1,$6,0,'{}',$7,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (raw_event_id) DO NOTHING`,
		rawEventID,
		event.Platform,
		conversationID,
		externalMessageID,
		[]byte(event.Prompt),
		contentHash[:],
		now,
		now.AddDate(0, 0, 60),
		event.ReplyTargetID,
		mustJSON(event.Context),
		hashJSON(event.Context),
		event.Context.HasMoreBefore,
		emptyStringAsNil(event.Context.HistoryCursor),
	)
	return errorValue
}

func connectorReplyOutboxID(rawEventID string, reply connectors.OutboundReply) string {
	document := strings.Join([]string{
		strings.TrimSpace(rawEventID),
		strings.TrimSpace(reply.TaskRunID),
		strings.TrimSpace(reply.ReplyKind),
		strings.TrimSpace(reply.Message),
	}, "\x00")
	digest := sha256.Sum256([]byte(document))
	return strings.TrimSpace(rawEventID) + ":reply:" + strings.TrimSpace(reply.ReplyKind) + ":" + hex.EncodeToString(digest[:8])
}

func (rawEventRepository RawEventRepository) EnqueueScheduledConnectorReply(taskSchedule task.TaskSchedule, taskRunID string, reply connectors.OutboundReply) (string, error) {
	conversationID, errorValue := NewConversationRepository(rawEventRepository.database).EnsureConversation(taskSchedule.Platform, taskSchedule.ConversationID)
	if errorValue != nil {
		return "", errorValue
	}
	rawEventID := "schedule:" + taskSchedule.TaskScheduleID + ":task:" + taskRunID
	reply.RawEventID = rawEventID
	reply.OutboxID = rawEventID
	if strings.TrimSpace(reply.TaskRunID) == "" {
		reply.TaskRunID = taskRunID
	}
	if strings.TrimSpace(reply.ReplyKind) == "" {
		reply.ReplyKind = "success"
	}
	replyTarget := connectors.ReplyTarget{
		ConversationID: taskSchedule.ConversationID,
		ReplyTargetID:  taskSchedule.ReplyTargetID,
		DedupeKey:      rawEventID,
	}
	replyTargetDocument, errorValue := json.Marshal(replyTarget)
	if errorValue != nil {
		return "", errorValue
	}
	replyDocument, errorValue := json.Marshal(reply)
	if errorValue != nil {
		return "", errorValue
	}
	now := time.Now().UTC()
	contentHash := sha256.Sum256([]byte(taskSchedule.Prompt))
	transaction, errorValue := rawEventRepository.database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return "", errorValue
	}
	defer transaction.Rollback()
	_, errorValue = transaction.ExecContext(context.Background(), `
INSERT INTO raw_event (
  raw_event_id, platform, conversation_id, external_message_id, event_type,
  content_ciphertext, encryption_key_version, content_sha256, security_level_rank,
  required_classes, occurred_at, ingested_at, expires_at,
  reply_target_id, visible_context_ciphertext, visible_context_sha256, has_more_before, history_cursor
) VALUES ($1,$2,$3,$1,'scheduled_task',$4,1,$5,0,'{}',$6,$6,$7,$8,$9,$10,false,NULL)
ON CONFLICT (raw_event_id) DO NOTHING`,
		rawEventID,
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
		return "", errorValue
	}
	_, errorValue = transaction.ExecContext(context.Background(), `
INSERT INTO connector_outbox (
  outbox_id, raw_event_id, platform, reply_target_id, reply_target_json, reply_json
) VALUES ($1,$1,$2,$3,$4,$5)
ON CONFLICT (outbox_id) DO NOTHING`,
		rawEventID,
		taskSchedule.Platform,
		taskSchedule.ReplyTargetID,
		replyTargetDocument,
		replyDocument,
	)
	if errorValue != nil {
		return "", errorValue
	}
	if errorValue = transaction.Commit(); errorValue != nil {
		return "", errorValue
	}
	return rawEventID, nil
}

func (rawEventRepository RawEventRepository) ClaimPendingConnectorReplies(limit int, leaseDuration time.Duration) ([]connectors.QueuedConnectorReply, error) {
	if limit <= 0 {
		limit = 1
	}
	now := time.Now().UTC()
	staleStartedAt := now.Add(-leaseDuration)
	rows, errorValue := rawEventRepository.database.SQL.QueryContext(context.Background(), `
WITH claim AS (
  SELECT outbox_id FROM connector_outbox
  WHERE (status = 'pending' AND next_attempt_at <= $1)
    OR (status = 'running' AND started_at <= $2)
  ORDER BY created_at ASC
  LIMIT $3
  FOR UPDATE SKIP LOCKED
)
UPDATE connector_outbox
SET status = 'running',
  started_at = $1,
  attempt_count = attempt_count + 1,
  last_error = '',
  updated_at = $1
WHERE outbox_id IN (SELECT outbox_id FROM claim)
RETURNING outbox_id, raw_event_id, platform, reply_target_json, reply_json, attempt_count`,
		now,
		staleStartedAt,
		limit,
	)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanQueuedConnectorReplies(rows)
}

func (rawEventRepository RawEventRepository) MarkConnectorReplySent(queuedReply connectors.QueuedConnectorReply, dispatchID string) error {
	_, errorValue := rawEventRepository.database.SQL.ExecContext(context.Background(), `
UPDATE connector_outbox
SET status = 'succeeded',
  dispatch_id = $1,
  completed_at = $2,
  updated_at = $2,
  last_error = ''
WHERE outbox_id = $3`,
		dispatchID,
		time.Now().UTC(),
		queuedReply.OutboxID,
	)
	return errorValue
}

func (rawEventRepository RawEventRepository) MarkConnectorReplyFailed(queuedReply connectors.QueuedConnectorReply, errorValue error, nextAttemptAt time.Time) error {
	status := "failed"
	if !nextAttemptAt.IsZero() {
		status = "pending"
	} else {
		nextAttemptAt = time.Now().UTC()
	}
	_, updateError := rawEventRepository.database.SQL.ExecContext(context.Background(), `
UPDATE connector_outbox
SET status = $1,
  next_attempt_at = $2,
  completed_at = $3,
  updated_at = $3,
  last_error = $4
WHERE outbox_id = $5`,
		status,
		nextAttemptAt,
		time.Now().UTC(),
		errorValue.Error(),
		queuedReply.OutboxID,
	)
	return updateError
}

func mustJSON(value any) []byte {
	document, _ := json.Marshal(value)
	return document
}

func hashJSON(value any) []byte {
	sum := sha256.Sum256(mustJSON(value))
	return sum[:]
}

func connectorResultReason(status string) string {
	switch status {
	case "pending":
		return "queued"
	case "running":
		return "running"
	case "failed":
		return "failed"
	default:
		return status
	}
}

type connectorEventDocument struct {
	Platform      string                          `json:"platform"`
	Source        string                          `json:"source"`
	RawReceivedAt time.Time                       `json:"rawReceivedAt"`
	Event         connectors.PlatformInboundEvent `json:"event"`
}

func marshalConnectorEvent(event connectors.PlatformInboundEvent) ([]byte, error) {
	return json.Marshal(connectorEventDocument{
		Platform:      event.Platform,
		Source:        event.Source,
		RawReceivedAt: event.RawReceivedAt,
		Event:         event,
	})
}

func unmarshalConnectorEvent(document []byte) (connectors.PlatformInboundEvent, error) {
	var envelope connectorEventDocument
	if errorValue := json.Unmarshal(document, &envelope); errorValue == nil && envelope.Event.MessageID != "" {
		event := envelope.Event
		event.Platform = envelope.Platform
		event.Source = envelope.Source
		event.RawReceivedAt = envelope.RawReceivedAt
		return event, nil
	}
	var event connectors.PlatformInboundEvent
	errorValue := json.Unmarshal(document, &event)
	return event, errorValue
}

func scanQueuedConnectorReplies(rows *sql.Rows) ([]connectors.QueuedConnectorReply, error) {
	queuedReplies := []connectors.QueuedConnectorReply{}
	for rows.Next() {
		queuedReply, errorValue := scanQueuedConnectorReply(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		queuedReplies = append(queuedReplies, queuedReply)
	}
	return queuedReplies, rows.Err()
}

func scanQueuedConnectorReply(scanner interface{ Scan(...any) error }) (connectors.QueuedConnectorReply, error) {
	var queuedReply connectors.QueuedConnectorReply
	var replyTargetDocument []byte
	var replyDocument []byte
	errorValue := scanner.Scan(
		&queuedReply.OutboxID,
		&queuedReply.RawEventID,
		&queuedReply.Platform,
		&replyTargetDocument,
		&replyDocument,
		&queuedReply.AttemptCount,
	)
	if errorValue != nil {
		return connectors.QueuedConnectorReply{}, errorValue
	}
	if errorValue := json.Unmarshal(replyTargetDocument, &queuedReply.ReplyTarget); errorValue != nil {
		return connectors.QueuedConnectorReply{}, errorValue
	}
	if errorValue := json.Unmarshal(replyDocument, &queuedReply.Reply); errorValue != nil {
		return connectors.QueuedConnectorReply{}, errorValue
	}
	return queuedReply, nil
}

func (rawEventRepository RawEventRepository) findConnectorOutboxID(rawEventID string) (string, error) {
	var outboxID string
	errorValue := rawEventRepository.database.SQL.QueryRowContext(context.Background(), `
SELECT outbox_id FROM connector_outbox WHERE raw_event_id = $1`, rawEventID).Scan(&outboxID)
	return outboxID, errorValue
}
