package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/llmexchange"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type LLMCallRepository struct {
	database Database
}

type splitDocuments struct {
	roots []sql.NullString
	parts []llmexchange.Part
}

func NewLLMCallRepository(database Database) LLMCallRepository {
	return LLMCallRepository{database: database}
}

func (repository LLMCallRepository) InsertLLMCall(taskEvent task.TaskEvent, record agentcontract.LLMCallRecord) error {
	return repository.insert(taskEvent, sql.NullString{String: taskEvent.TaskRunID, Valid: taskEvent.TaskRunID != ""}, nil, record)
}

func (repository LLMCallRepository) InsertTasklessLLMCall(taskEvent task.TaskEvent, subjects []string, record agentcontract.LLMCallRecord) error {
	return repository.insert(taskEvent, sql.NullString{}, subjects, record)
}

func (repository LLMCallRepository) insert(taskEvent task.TaskEvent, taskRunID sql.NullString, subjects []string, record agentcontract.LLMCallRecord) error {
	split, errorValue := splitEach(callDocuments(record)...)
	if errorValue != nil {
		return fmt.Errorf("split the documents of llm call %s: %w", taskEvent.TaskEventID, errorValue)
	}
	return withTransaction(repository.database, func(transaction *sql.Tx) error {
		if errorValue := insertLedgerParts(transaction, split.parts); errorValue != nil {
			return errorValue
		}
		_, errorValue := transaction.Exec(`
INSERT INTO llm_call (llm_call_id, task_run_id, subjects, record, request_part, response_part, input_part, part_hashes, created_at)
VALUES ($1,$2,coalesce($3::text[], '{}'),$4,$5,$6,$7,$8,$9)
ON CONFLICT (llm_call_id) DO NOTHING`,
			taskEvent.TaskEventID, taskRunID, subjects, taskEvent.Body, split.roots[0], split.roots[1], split.roots[2], partHashes(split.parts), taskEvent.CreatedAt)
		return errorValue
	})
}

func callDocuments(record agentcontract.LLMCallRecord) []string {
	documents := []string{"", "", string(record.Input)}
	if record.Exchange != nil {
		documents[0] = record.Exchange.Request
		documents[1] = record.Exchange.Response
	}
	return documents
}

func splitEach(documents ...string) (splitDocuments, error) {
	split := splitDocuments{}
	for _, document := range documents {
		if document == "" {
			split.roots = append(split.roots, sql.NullString{})
			continue
		}
		rootHash, parts, errorValue := llmexchange.Split(document)
		if errorValue != nil {
			return splitDocuments{}, errorValue
		}
		split.roots = append(split.roots, sql.NullString{String: rootHash, Valid: true})
		split.parts = append(split.parts, parts...)
	}
	return split, nil
}

func withTransaction(database Database, work func(*sql.Tx) error) error {
	transaction, errorValue := database.SQL.BeginTx(context.Background(), nil)
	if errorValue != nil {
		return errorValue
	}
	defer transaction.Rollback()
	if errorValue := work(transaction); errorValue != nil {
		return errorValue
	}
	return transaction.Commit()
}

func insertLedgerParts(transaction *sql.Tx, parts []llmexchange.Part) error {
	hashes := make([]string, 0, len(parts))
	bodies := make([]string, 0, len(parts))
	for _, part := range parts {
		hashes = append(hashes, part.Hash)
		bodies = append(bodies, part.Body)
	}
	_, errorValue := transaction.Exec(`
INSERT INTO ledger_part (part_hash, body)
SELECT * FROM unnest($1::text[], $2::text[])
ON CONFLICT (part_hash) DO NOTHING`, hashes, bodies)
	return errorValue
}

func partHashes(parts []llmexchange.Part) []string {
	seen := map[string]bool{}
	hashes := make([]string, 0, len(parts))
	for _, part := range parts {
		if seen[part.Hash] {
			continue
		}
		seen[part.Hash] = true
		hashes = append(hashes, part.Hash)
	}
	return hashes
}

func (repository LLMCallRepository) FindLLMCallExchange(llmCallID string) (llmexchange.Exchange, bool, error) {
	var requestPart, responsePart, inputPart sql.NullString
	errorValue := repository.database.SQL.QueryRowContext(context.Background(),
		`SELECT request_part, response_part, input_part FROM llm_call WHERE llm_call_id = $1`, llmCallID).Scan(&requestPart, &responsePart, &inputPart)
	if errors.Is(errorValue, sql.ErrNoRows) || (errorValue == nil && !requestPart.Valid && !inputPart.Valid) {
		return llmexchange.Exchange{}, false, nil
	}
	if errorValue != nil {
		return llmexchange.Exchange{}, false, errorValue
	}
	joined, errorValue := joinEach(lookupLedgerParts(repository.database), requestPart, responsePart, inputPart)
	if errorValue != nil {
		return llmexchange.Exchange{}, false, errorValue
	}
	return llmexchange.Exchange{Request: joined[0], Response: joined[1], Input: joined[2]}, true, nil
}

func joinEach(lookup llmexchange.PartLookup, roots ...sql.NullString) ([]string, error) {
	joined := make([]string, len(roots))
	for index, root := range roots {
		if !root.Valid {
			continue
		}
		document, errorValue := llmexchange.Join(root.String, lookup)
		if errorValue != nil {
			return nil, errorValue
		}
		joined[index] = document
	}
	return joined, nil
}

func lookupLedgerParts(database Database) llmexchange.PartLookup {
	return func(hashes []string) (map[string]string, error) {
		rows, errorValue := database.SQL.QueryContext(context.Background(),
			`SELECT part_hash, body FROM ledger_part WHERE part_hash = ANY($1::text[])`, hashes)
		if errorValue != nil {
			return nil, errorValue
		}
		defer rows.Close()
		bodies := map[string]string{}
		for rows.Next() {
			var hash, body string
			if errorValue := rows.Scan(&hash, &body); errorValue != nil {
				return nil, errorValue
			}
			bodies[hash] = body
		}
		return bodies, rows.Err()
	}
}

func (repository LLMCallRepository) PruneTasklessLLMCallsBefore(cutoff time.Time) (int64, error) {
	result, errorValue := repository.database.SQL.ExecContext(context.Background(),
		`DELETE FROM llm_call WHERE task_run_id IS NULL AND created_at < $1`, cutoff)
	if errorValue != nil {
		return 0, errorValue
	}
	return result.RowsAffected()
}

func (repository LLMCallRepository) DeleteUnreferencedLedgerParts() (int64, error) {
	result, errorValue := repository.database.SQL.ExecContext(context.Background(), `
DELETE FROM ledger_part part
WHERE NOT EXISTS (SELECT 1 FROM llm_call WHERE llm_call.part_hashes @> ARRAY[part.part_hash])
  AND NOT EXISTS (SELECT 1 FROM task_event WHERE task_event.part_hashes @> ARRAY[part.part_hash])`)
	if errorValue != nil {
		return 0, errorValue
	}
	return result.RowsAffected()
}
