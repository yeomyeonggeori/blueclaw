package memory

import (
	"time"
)

const (
	JobKindExtract = "extract"
	JobKindProfile = "profile"
	JobKindReembed = "reembed"
	JobKindImport  = "import"
)

const (
	DefaultJobMaxAttempts   = 5
	DefaultJobLeaseDuration = 5 * time.Minute
	jobRetryBaseDelay       = time.Minute
	jobRetryMaximumDelay    = 30 * time.Minute
)

type Job struct {
	JobID       string    `json:"jobID"`
	Kind        string    `json:"kind"`
	SubjectID   string    `json:"subjectID"`
	Attempts    int       `json:"attempts"`
	RunAfter    time.Time `json:"runAfter"`
	LockedUntil time.Time `json:"lockedUntil,omitzero"`
	LastError   string    `json:"lastError,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	FinishedAt  time.Time `json:"finishedAt,omitzero"`
}

type TerminalJobError struct {
	Cause error
}

func (errorValue TerminalJobError) Error() string {
	return errorValue.Cause.Error()
}

func (errorValue TerminalJobError) Unwrap() error {
	return errorValue.Cause
}

func JobRetryDelay(attempts int) time.Duration {
	if attempts <= 1 {
		return jobRetryBaseDelay
	}
	delay := jobRetryBaseDelay
	for attempt := 1; attempt < attempts; attempt++ {
		delay *= 2
		if delay >= jobRetryMaximumDelay {
			return jobRetryMaximumDelay
		}
	}
	return delay
}
