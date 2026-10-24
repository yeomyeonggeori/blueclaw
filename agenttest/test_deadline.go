package agenttest

import (
	"testing"
	"time"
)

const timeLeftToReportAFailure = 5 * time.Second

func WhenTheTestRunsOut(t *testing.T) <-chan time.Time {
	deadline, hasDeadline := t.Deadline()
	if !hasDeadline {
		return nil
	}
	return time.After(time.Until(deadline) - timeLeftToReportAFailure)
}
