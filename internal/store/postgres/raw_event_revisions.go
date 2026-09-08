package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
)

func suppressSupersededConnectorEvents(transaction *sql.Tx, previousMessages []connectors.PendingRequestMessage) error {
	sourceReferences := []string{}
	for _, message := range previousMessages {
		sourceReference := strings.TrimSpace(message.SourceReference)
		if sourceReference != "" {
			sourceReferences = append(sourceReferences, sourceReference)
		}
	}
	if len(sourceReferences) == 0 {
		return nil
	}

	_, errorValue := transaction.ExecContext(context.Background(), `
UPDATE raw_event
SET connector_status = 'succeeded',
  connector_completed_at = NOW(),
  connector_result_json = COALESCE(connector_result_json, '{}'::jsonb)
    || jsonb_build_object('handled', true, 'reason', $2::text),
  connector_error = ''
WHERE raw_event_id = ANY($1::text[])`, sourceReferences, connectors.SupersededRequestReason)
	if errorValue != nil {
		return errorValue
	}

	_, errorValue = transaction.ExecContext(context.Background(), `
UPDATE connector_outbox
SET status = 'suppressed',
  completed_at = NOW(),
  updated_at = NOW(),
  last_error = $2
WHERE raw_event_id = ANY($1::text[])
  AND status IN ('pending', 'running')`, sourceReferences, connectors.SupersededRequestReason)
	return errorValue
}

func (rawEventRepository RawEventRepository) ListUnansweredConnectorEvents() ([]connectors.PlatformInboundEvent, error) {
	rows, errorValue := rawEventRepository.database.SQL.QueryContext(context.Background(), `
SELECT connector_event_json
FROM raw_event
WHERE connector_event_json != '{}'::jsonb
  AND (
    connector_status IN ('pending', 'running')
    OR (
      connector_status = 'succeeded'
      AND EXISTS (
        SELECT 1
        FROM connector_outbox
        WHERE connector_outbox.raw_event_id = raw_event.raw_event_id
          AND connector_outbox.status IN ('pending', 'running')
      )
    )
  )
  AND NOT EXISTS (
    SELECT 1
    FROM connector_outbox
    WHERE connector_outbox.raw_event_id = raw_event.raw_event_id
      AND connector_outbox.status = 'succeeded'
  )
ORDER BY ingested_at ASC, raw_event_id ASC`)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()

	events := []connectors.PlatformInboundEvent{}
	for rows.Next() {
		var eventDocument json.RawMessage
		if errorValue := rows.Scan(&eventDocument); errorValue != nil {
			return nil, errorValue
		}
		event, errorValue := unmarshalConnectorEvent(eventDocument)
		if errorValue != nil {
			return nil, errorValue
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (rawEventRepository RawEventRepository) SuppressConnectorReply(queuedReply connectors.QueuedConnectorReply, reason string) error {
	_, errorValue := rawEventRepository.database.SQL.ExecContext(context.Background(), `
UPDATE connector_outbox
SET status = 'suppressed',
  completed_at = NOW(),
  updated_at = NOW(),
  last_error = $1
WHERE outbox_id = $2
  AND status IN ('pending', 'running')`,
		reason,
		queuedReply.OutboxID,
	)
	return errorValue
}
