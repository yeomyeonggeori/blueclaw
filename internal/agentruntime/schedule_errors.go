package agentruntime

import "errors"

var errScheduleCreateInScheduledRun = errors.New("scheduled task executions cannot create new schedules")
