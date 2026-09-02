package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

type MemoryJobRepository struct {
	database Database
}

func NewMemoryJobRepository(database Database) MemoryJobRepository {
	return MemoryJobRepository{database: database}
}

const memoryJobColumns = `
  job_id, kind, subject_id, attempts, run_after, locked_until, COALESCE(last_error, ''), created_at, finished_at`

func (repository MemoryJobRepository) EnqueueJob(ctx context.Context, kind string, subjectID string, runAfter time.Time) (memory.Job, bool, error) {
	row := repository.database.SQL.QueryRowContext(ctx, `
INSERT INTO memory_job (job_id, kind, subject_id, run_after)
VALUES ($1, $2, $3, $4)
ON CONFLICT (kind, subject_id) WHERE finished_at IS NULL DO NOTHING
RETURNING`+memoryJobColumns, taskstate.NewIdentifier(), kind, subjectID, runAfter.UTC())
	job, errorValue := scanMemoryJob(row)
	if errorValue == nil {
		return job, true, nil
	}
	if !errors.Is(errorValue, sql.ErrNoRows) {
		return memory.Job{}, false, errorValue
	}
	pendingJob, errorValue := scanMemoryJob(repository.database.SQL.QueryRowContext(ctx, `
SELECT`+memoryJobColumns+`
FROM memory_job WHERE kind = $1 AND subject_id = $2 AND finished_at IS NULL`, kind, subjectID))
	return pendingJob, false, errorValue
}

func (repository MemoryJobRepository) ClaimDueJobs(ctx context.Context, kinds []string, referenceTime time.Time, leaseDuration time.Duration, limit int) ([]memory.Job, error) {
	if len(kinds) == 0 || limit <= 0 {
		return []memory.Job{}, nil
	}
	rows, errorValue := repository.database.SQL.QueryContext(ctx, `
WITH due AS (
  SELECT job_id FROM memory_job
  WHERE finished_at IS NULL
    AND kind = ANY($1::text[])
    AND run_after <= $2
    AND (locked_until IS NULL OR locked_until <= $2)
  ORDER BY run_after ASC
  LIMIT $4
  FOR UPDATE SKIP LOCKED
)
UPDATE memory_job job SET locked_until = $3, attempts = job.attempts + 1
FROM due WHERE job.job_id = due.job_id
RETURNING job.job_id, job.kind, job.subject_id, job.attempts, job.run_after, job.locked_until, COALESCE(job.last_error, ''), job.created_at, job.finished_at`,
		nonNilStrings(kinds), referenceTime.UTC(), referenceTime.Add(leaseDuration).UTC(), limit)
	if errorValue != nil {
		return nil, errorValue
	}
	defer rows.Close()
	jobs := []memory.Job{}
	for rows.Next() {
		job, errorValue := scanMemoryJob(rows)
		if errorValue != nil {
			return nil, errorValue
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (repository MemoryJobRepository) FinishJob(ctx context.Context, jobID string, finishedAt time.Time) error {
	_, errorValue := repository.database.SQL.ExecContext(ctx, `
UPDATE memory_job SET finished_at = $2, locked_until = NULL, last_error = NULL WHERE job_id = $1`, jobID, finishedAt.UTC())
	return errorValue
}

func (repository MemoryJobRepository) RetryJob(ctx context.Context, jobID string, lastError string, runAfter time.Time) error {
	_, errorValue := repository.database.SQL.ExecContext(ctx, `
UPDATE memory_job SET run_after = $3, locked_until = NULL, last_error = $2 WHERE job_id = $1`, jobID, lastError, runAfter.UTC())
	return errorValue
}

func (repository MemoryJobRepository) AbandonJob(ctx context.Context, jobID string, lastError string, finishedAt time.Time) error {
	_, errorValue := repository.database.SQL.ExecContext(ctx, `
UPDATE memory_job SET finished_at = $3, locked_until = NULL, last_error = $2 WHERE job_id = $1`, jobID, lastError, finishedAt.UTC())
	return errorValue
}

func (repository MemoryJobRepository) FindJob(ctx context.Context, jobID string) (memory.Job, bool, error) {
	job, errorValue := scanMemoryJob(repository.database.SQL.QueryRowContext(ctx, `
SELECT`+memoryJobColumns+`
FROM memory_job WHERE job_id = $1`, jobID))
	if errors.Is(errorValue, sql.ErrNoRows) {
		return memory.Job{}, false, nil
	}
	return job, errorValue == nil, errorValue
}

type rowScanner interface {
	Scan(targets ...any) error
}

func scanMemoryJob(row rowScanner) (memory.Job, error) {
	var job memory.Job
	var lockedUntil, finishedAt sql.NullTime
	errorValue := row.Scan(&job.JobID, &job.Kind, &job.SubjectID, &job.Attempts, &job.RunAfter, &lockedUntil, &job.LastError, &job.CreatedAt, &finishedAt)
	if errorValue != nil {
		return memory.Job{}, errorValue
	}
	job.RunAfter = job.RunAfter.UTC()
	job.CreatedAt = job.CreatedAt.UTC()
	job.LockedUntil = timeFromNullable(lockedUntil)
	job.FinishedAt = timeFromNullable(finishedAt)
	return job, nil
}
