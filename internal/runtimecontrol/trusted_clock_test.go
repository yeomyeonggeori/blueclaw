package runtimecontrol

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKernelSynchronizationTrustsTheClockWithoutAWitness(t *testing.T) {
	clock := clockWithWitness(t, false, time.Now())

	if _, isTrusted := clock.check(context.Background()); !isTrusted {
		t.Fatal("a clock the kernel synchronized must be trusted")
	}
}

func TestWitnessAgreementTrustsAClockTheKernelDoubts(t *testing.T) {
	clock := clockWithWitness(t, true, time.Now())

	if _, isTrusted := clock.check(context.Background()); !isTrusted {
		t.Fatal("a doubted clock that agrees with its witness must be trusted")
	}
}

func TestWitnessDisagreementHoldsTaskIntake(t *testing.T) {
	clock := clockWithWitness(t, true, time.Now().Add(8*time.Hour))
	controller := NewTaskIntakeController(clock)

	readings, isTrusted := clock.check(context.Background())

	if isTrusted || !controller.IsQuiesced() {
		t.Fatal("a clock eight hours off its witness must hold task intake")
	}
	if len(readings) != 1 || readings[0].offset > -7*time.Hour {
		t.Fatalf("the reading must carry how far the clock is off, got %+v", readings)
	}
}

func TestNoWitnessStartsWorkRatherThanWaitingForever(t *testing.T) {
	clock := NewTrustedClock([]string{"http://127.0.0.1:8080/v1"}, slog.New(slog.DiscardHandler))
	clock.kernelDoubtsTheClock = func() bool { return true }
	controller := NewTaskIntakeController(clock)

	clock.check(context.Background())

	if controller.IsQuiesced() {
		t.Fatal("a clock nothing can ever witness must not hold task intake forever")
	}
}

func TestUnreachableWitnessHoldsTaskIntake(t *testing.T) {
	clock := NewTrustedClock([]string{"https://127.0.0.1:1/v1"}, slog.New(slog.DiscardHandler))
	clock.kernelDoubtsTheClock = func() bool { return true }
	controller := NewTaskIntakeController(clock)

	readings, isTrusted := clock.check(context.Background())

	if isTrusted || !controller.IsQuiesced() || len(readings) != 1 || readings[0].failed == nil {
		t.Fatalf("a witness that cannot be reached must hold task intake, got %+v", readings)
	}
}

func TestTaskIntakeOpensOnceTheClockIsTrusted(t *testing.T) {
	clock := clockWithWitness(t, false, time.Now())
	controller := NewTaskIntakeController(clock)
	if !controller.IsQuiesced() {
		t.Fatal("task intake must wait until the clock has been checked")
	}

	clock.check(context.Background())

	if controller.IsQuiesced() {
		t.Fatal("task intake must open once the clock is trusted")
	}
}

func TestOnlyTLSOriginsWitnessTheClock(t *testing.T) {
	origins := witnessOriginsOf([]string{
		"https://models.example.com/api/v1/chat/completions",
		"https://models.example.com/api/v1/embeddings",
		"http://127.0.0.1:8080/v1",
		"",
	})

	if len(origins) != 1 || origins[0] != "https://models.example.com" {
		t.Fatalf("only distinct https origins witness the clock, got %v", origins)
	}
}

func clockWithWitness(t *testing.T, kernelDoubts bool, witnessTime time.Time) *TrustedClock {
	t.Helper()
	witness := httptest.NewTLSServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Date", witnessTime.UTC().Format(http.TimeFormat))
		responseWriter.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(witness.Close)
	clock := NewTrustedClock([]string{witness.URL + "/api/v1"}, slog.New(slog.DiscardHandler))
	clock.httpClient = witness.Client()
	clock.kernelDoubtsTheClock = func() bool { return kernelDoubts }
	return clock
}
