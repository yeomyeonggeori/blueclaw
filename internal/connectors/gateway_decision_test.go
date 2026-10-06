package connectors

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

func TestAFailedGatewayDecisionIsLoggedAndTheTurnStillLaunches(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	connectorRuntimeHarness := harnesstest.New(taskRunService)
	connectorRuntime, adapter := connectorRuntimeForHarness(
		t,
		connectorRuntimeHarness,
		&scriptedGatewayDecider{errorValue: errors.New("decision model unavailable")},
		connectorRuntimeHarness,
		taskRunService,
		testLanguageModel{reply: "stub"},
	)
	loggedLines := &strings.Builder{}
	connectorRuntime.logger = slog.New(slog.NewTextHandler(loggedLines, &slog.HandlerOptions{Level: slog.LevelWarn}))

	mentionedEvent := testChannelInboundEvent("message-1")
	mentionedEvent.Context.Addressing.BotMentioned = true
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, mentionedEvent); errorValue != nil {
		t.Fatalf("expected the turn to launch: %v", errorValue)
	}

	logged := loggedLines.String()
	if !strings.Contains(logged, "connector.test.addressing.decision_failed") {
		t.Fatalf("expected the failed decision to be logged, got %q", logged)
	}
	if !strings.Contains(logged, "message-1") || !strings.Contains(logged, "decision model unavailable") {
		t.Fatalf("expected the log line to name the message and the error, got %q", logged)
	}
	if connectorRuntimeHarness.RunTurnCallCount() != 1 {
		t.Fatalf("expected the turn to run once after the failed decision, got %d", connectorRuntimeHarness.RunTurnCallCount())
	}
}
