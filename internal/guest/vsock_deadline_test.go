package guest

import (
	"context"
	"testing"
	"time"
)

func TestSoonestDeadlineUsesContextDeadlineWhenEarlier(t *testing.T) {
	now := time.Unix(100, 0)
	contextDeadline := now.Add(500 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), contextDeadline)
	defer cancel()

	deadline := soonestDeadline(now, ctx, 2*time.Second)
	if !deadline.Equal(contextDeadline) {
		t.Fatalf("expected context deadline, got %s", deadline)
	}
}

func TestSoonestDeadlineUsesTimeoutWhenContextDeadlineIsLater(t *testing.T) {
	now := time.Unix(100, 0)
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(5*time.Second))
	defer cancel()

	deadline := soonestDeadline(now, ctx, 2*time.Second)
	expectedDeadline := now.Add(2 * time.Second)
	if !deadline.Equal(expectedDeadline) {
		t.Fatalf("expected timeout deadline, got %s", deadline)
	}
}

func TestSoonestDeadlineUsesTimeoutWithoutContextDeadline(t *testing.T) {
	now := time.Unix(100, 0)

	deadline := soonestDeadline(now, context.Background(), 2*time.Second)
	expectedDeadline := now.Add(2 * time.Second)
	if !deadline.Equal(expectedDeadline) {
		t.Fatalf("expected timeout deadline, got %s", deadline)
	}
}
