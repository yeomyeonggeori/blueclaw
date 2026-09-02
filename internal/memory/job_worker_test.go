package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeJobRepository struct {
	due        []Job
	claimed    []string
	finished   []string
	retried    map[string]time.Time
	abandoned  map[string]string
	claimKinds []string
}

func newFakeJobRepository(due ...Job) *fakeJobRepository {
	return &fakeJobRepository{due: due, retried: map[string]time.Time{}, abandoned: map[string]string{}}
}

func (repository *fakeJobRepository) EnqueueJob(context.Context, string, string, time.Time) (Job, bool, error) {
	return Job{}, false, errors.New("not used")
}

func (repository *fakeJobRepository) ClaimDueJobs(_ context.Context, kinds []string, _ time.Time, _ time.Duration, _ int) ([]Job, error) {
	repository.claimKinds = kinds
	claimed := []Job{}
	for _, job := range repository.due {
		if containsString(kinds, job.Kind) {
			job.Attempts++
			claimed = append(claimed, job)
			repository.claimed = append(repository.claimed, job.JobID)
		}
	}
	return claimed, nil
}

func (repository *fakeJobRepository) FinishJob(_ context.Context, jobID string, _ time.Time) error {
	repository.finished = append(repository.finished, jobID)
	return nil
}

func (repository *fakeJobRepository) RetryJob(_ context.Context, jobID string, _ string, runAfter time.Time) error {
	repository.retried[jobID] = runAfter
	return nil
}

func (repository *fakeJobRepository) AbandonJob(_ context.Context, jobID string, lastError string, _ time.Time) error {
	repository.abandoned[jobID] = lastError
	return nil
}

func TestJobWorkerClaimsOnlyHandledKindsAndFinishesSuccesses(t *testing.T) {
	repository := newFakeJobRepository(Job{JobID: "extract-1", Kind: JobKindExtract}, Job{JobID: "profile-1", Kind: JobKindProfile})
	worker := JobWorker{Jobs: repository, Handlers: map[string]JobHandler{
		JobKindExtract: func(context.Context, Job) error { return nil },
	}}
	runCount, errorValue := worker.RunOnce(context.Background())
	if errorValue != nil || runCount != 1 {
		t.Fatalf("expected one job to run, got %d (%v)", runCount, errorValue)
	}
	if len(repository.claimKinds) != 1 || repository.claimKinds[0] != JobKindExtract {
		t.Fatalf("expected only the extract kind to be claimed, got %v", repository.claimKinds)
	}
	if len(repository.finished) != 1 || repository.finished[0] != "extract-1" {
		t.Fatalf("expected extract-1 to be finished, got %v", repository.finished)
	}
}

func TestJobWorkerRetriesWithBackoffUntilAttemptsRunOut(t *testing.T) {
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	repository := newFakeJobRepository(Job{JobID: "extract-1", Kind: JobKindExtract, Attempts: 1})
	worker := JobWorker{Jobs: repository, MaxAttempts: 3, Now: func() time.Time { return now }, Handlers: map[string]JobHandler{
		JobKindExtract: func(context.Context, Job) error { return errors.New("model unavailable") },
	}}
	if _, errorValue := worker.RunOnce(context.Background()); errorValue != nil {
		t.Fatal(errorValue)
	}
	if runAfter, isRetried := repository.retried["extract-1"]; !isRetried || runAfter != now.Add(JobRetryDelay(2)) {
		t.Fatalf("expected a retry at %s, got %v", now.Add(JobRetryDelay(2)), repository.retried)
	}
	repository.due[0].Attempts = 2
	if _, errorValue := worker.RunOnce(context.Background()); errorValue != nil {
		t.Fatal(errorValue)
	}
	if repository.abandoned["extract-1"] != "model unavailable" {
		t.Fatalf("expected the third attempt to abandon the job, got %v", repository.abandoned)
	}
}

func TestJobWorkerAbandonsOnATerminalError(t *testing.T) {
	repository := newFakeJobRepository(Job{JobID: "extract-1", Kind: JobKindExtract})
	worker := JobWorker{Jobs: repository, Handlers: map[string]JobHandler{
		JobKindExtract: func(context.Context, Job) error {
			return TerminalJobError{Cause: errors.New("supersedes an unknown fact")}
		},
	}}
	if _, errorValue := worker.RunOnce(context.Background()); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(repository.retried) != 0 || repository.abandoned["extract-1"] != "supersedes an unknown fact" {
		t.Fatalf("expected an immediate abandon, got retried=%v abandoned=%v", repository.retried, repository.abandoned)
	}
}

func TestJobWorkerWithoutHandlersClaimsNothing(t *testing.T) {
	repository := newFakeJobRepository(Job{JobID: "extract-1", Kind: JobKindExtract})
	runCount, errorValue := JobWorker{Jobs: repository}.RunOnce(context.Background())
	if errorValue != nil || runCount != 0 || len(repository.claimed) != 0 {
		t.Fatalf("expected no claims without handlers, got %d claimed=%v (%v)", runCount, repository.claimed, errorValue)
	}
}
