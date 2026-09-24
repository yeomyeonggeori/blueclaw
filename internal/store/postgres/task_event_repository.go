package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yeomyeonggeori/blueclaw/internal/llmexchange"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TaskEventRepository struct {
	database Database
}

func NewTaskEventRepository(database Database) TaskEventRepository {
	return TaskEventRepository{database: database}
}

func (taskEventRepository TaskEventRepository) InsertTaskEvent(taskEvent task.TaskEvent) error {
	_, errorValue := taskEventRepository.database.SQL.ExecContext(context.Background(), `
INSERT INTO task_event (task_event_id, task_run_id, name, body, created_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (task_event_id) DO NOTHING`,
		taskEvent.TaskEventID,
		taskEvent.TaskRunID,
		taskEvent.Name,
		taskEvent.Body,
		taskEvent.CreatedAt,
	)
	return errorValue
}

func (taskEventRepository TaskEventRepository) InsertPartedTaskEvent(taskEvent task.TaskEvent, document json.RawMessage) error {
	rootHash, parts, errorValue := llmexchange.Split(string(document))
	if errorValue != nil {
		return fmt.Errorf("split task event %s: %w", taskEvent.TaskEventID, errorValue)
	}
	return withTransaction(taskEventRepository.database, func(transaction *sql.Tx) error {
		if errorValue := insertLedgerParts(transaction, parts); errorValue != nil {
			return errorValue
		}
		_, errorValue := transaction.Exec(`
INSERT INTO task_event (task_event_id, task_run_id, name, body, part_hashes, created_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (task_event_id) DO NOTHING`,
			taskEvent.TaskEventID, taskEvent.TaskRunID, taskEvent.Name, llmexchange.Reference(rootHash), partHashes(parts), taskEvent.CreatedAt)
		return errorValue
	})
}

func (taskEventRepository TaskEventRepository) FindPartedTaskEventDocument(taskEventID string) (string, bool, error) {
	var body string
	errorValue := taskEventRepository.database.SQL.QueryRowContext(context.Background(),
		`SELECT body FROM task_event WHERE task_event_id = $1 AND part_hashes IS NOT NULL`, taskEventID).Scan(&body)
	if errors.Is(errorValue, sql.ErrNoRows) {
		return "", false, nil
	}
	if errorValue != nil {
		return "", false, errorValue
	}
	document, errorValue := llmexchange.Expand(body, lookupLedgerParts(taskEventRepository.database))
	return document, errorValue == nil, errorValue
}

func (taskEventRepository TaskEventRepository) ListTaskEvent(taskRunID string) ([]task.TaskEvent, error) {
	rows, errorValue := taskEventRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_event_id, task_run_id, name, body, created_at
FROM task_event WHERE task_run_id = $1
UNION ALL
SELECT llm_call_id, task_run_id, '`+agentcontract.TaskEventLLMCall+`', record, created_at
FROM llm_call WHERE task_run_id = $1
ORDER BY created_at ASC`, taskRunID)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskEvents(rows)
}

func (taskEventRepository TaskEventRepository) ListTaskEventByNameForTaskRuns(taskRunIDs []string, name string) ([]task.TaskEvent, error) {
	if len(taskRunIDs) == 0 || name == "" {
		return []task.TaskEvent{}, nil
	}
	rows, errorValue := taskEventRepository.database.SQL.QueryContext(context.Background(), `
SELECT task_event_id, task_run_id, name, body, created_at
FROM task_event
WHERE task_run_id = ANY($1::text[]) AND name = $2
UNION ALL
SELECT llm_call_id, task_run_id, $2::text, record, created_at
FROM llm_call
WHERE task_run_id = ANY($1::text[]) AND $2 = '`+agentcontract.TaskEventLLMCall+`'
ORDER BY created_at ASC`, taskRunIDs, name)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	return scanTaskEvents(rows)
}

func scanTaskEvents(rows *sql.Rows) ([]task.TaskEvent, error) {
	taskEvents := []task.TaskEvent{}
	for rows.Next() {
		var taskEvent task.TaskEvent
		if errorValue := rows.Scan(&taskEvent.TaskEventID, &taskEvent.TaskRunID, &taskEvent.Name, &taskEvent.Body, &taskEvent.CreatedAt); errorValue != nil {
			return nil, errorValue
		}
		taskEvents = append(taskEvents, taskEvent)
	}
	return taskEvents, rows.Err()
}
