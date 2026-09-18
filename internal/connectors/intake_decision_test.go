package connectors

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

type countingTurnRouter struct {
	inner            TurnRouter
	planCount        int
	sawDecidedFields bool
}

func (router *countingTurnRouter) Plan(ctx context.Context, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	return router.PlanObserved(ctx, request, nil)
}

func (router *countingTurnRouter) PlanObserved(ctx context.Context, request agentcontract.AgentRequest, callLedger *agentcontract.IntakeCallLedger) (agentcontract.TurnDecision, error) {
	router.planCount++
	router.sawDecidedFields = router.sawDecidedFields || request.DecidedTurnFields != nil
	return router.inner.PlanObserved(ctx, request, callLedger)
}

func TestAFailedDecisionIsLoggedAndLeavesTheRouterOneDecision(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	connectorRuntimeHarness := harnesstest.New(taskRunService)
	router := &countingTurnRouter{inner: connectorRuntimeHarness}
	connectorRuntime, adapter := connectorRuntimeForHarness(
		t,
		connectorRuntimeHarness,
		&scriptedIntakeDecider{errorValue: errors.New("decision model unavailable")},
		connectorRuntimeHarness,
		router,
		taskRunService,
		testLanguageModel{reply: "stub"},
	)
	loggedLines := &strings.Builder{}
	connectorRuntime.logger = slog.New(slog.NewTextHandler(loggedLines, &slog.HandlerOptions{Level: slog.LevelWarn}))

	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1")); errorValue != nil {
		t.Fatalf("expected the turn to launch through the router: %v", errorValue)
	}

	logged := loggedLines.String()
	if !strings.Contains(logged, "connector.test.intake.decision_failed") {
		t.Fatalf("expected the failed decision to be logged, got %q", logged)
	}
	if !strings.Contains(logged, "message-1") || !strings.Contains(logged, "decision model unavailable") {
		t.Fatalf("expected the log line to name the message and the error, got %q", logged)
	}
	if router.planCount != 1 {
		t.Fatalf("expected the router to be asked once after the failed decision, got %d", router.planCount)
	}
	if router.sawDecidedFields {
		t.Fatal("expected the router to decide for itself when the decision failed")
	}
}
