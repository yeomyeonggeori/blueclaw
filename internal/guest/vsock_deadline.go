package guest

import (
	"context"
	"time"
)

const guestVSockOperationTimeout = 2 * time.Second

func soonestDeadline(now time.Time, ctx context.Context, timeout time.Duration) time.Time {
	deadline := now.Add(timeout)
	contextDeadline, hasContextDeadline := ctx.Deadline()
	if hasContextDeadline && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}
