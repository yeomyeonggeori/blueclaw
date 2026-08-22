package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestShutdownWaitsForTheLoopsItStarted(t *testing.T) {
	application := &Application{}
	stoppedAt := make(chan time.Time, 1)
	cancel := application.startBackgroundLoop(func(ctx context.Context) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		stoppedAt <- time.Now()
	})

	cancel()
	if errorValue := application.awaitBackgroundLoops(context.Background()); errorValue != nil {
		t.Fatalf("waiting failed: %v", errorValue)
	}
	returnedAt := time.Now()

	select {
	case loopStoppedAt := <-stoppedAt:
		if returnedAt.Before(loopStoppedAt) {
			t.Fatal("shutdown returned before the loop stopped, which is how a sweeper writes to a database the next line closes")
		}
	default:
		t.Fatal("cancelling a context asks a goroutine to stop; waiting is what makes it stopped")
	}
}

func TestShutdownGivesUpLoudlyOnALoopThatWillNotStop(t *testing.T) {
	application := &Application{}
	release := make(chan struct{})
	defer close(release)
	application.startBackgroundLoop(func(context.Context) { <-release })

	deadlinedContext, cancelDeadline := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelDeadline()
	errorValue := application.awaitBackgroundLoops(deadlinedContext)

	if errorValue == nil || !strings.Contains(errorValue.Error(), "ran out of time") {
		t.Fatalf("a loop that never stops is reported and named, never waited on forever: %v", errorValue)
	}
}
