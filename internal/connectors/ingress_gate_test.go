package connectors

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

func newIngressGateTestRuntime(gate IngressGate) (*ConnectorRuntime, *task.TaskRunService) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	connectorRuntimeHarness := harnesstest.New(taskRunService)
	connectorRuntime := NewConnectorRuntime(identityService, connectorRuntimeHarness, taskRunService, taskEventService, slog.Default())
	connectorRuntime.UseIntakeClassifier(connectorRuntimeHarness)
	connectorRuntime.UseReplyGenerator(connectorRuntimeHarness)
	connectorRuntime.UseIngressGate(gate)
	return connectorRuntime, taskRunService
}

func shrinkIngressGateWaitBudget(t *testing.T) {
	t.Helper()
	originalBudget := ingressGateWaitBudget
	originalInterval := ingressGatePollInterval
	ingressGateWaitBudget = 100 * time.Millisecond
	ingressGatePollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		ingressGateWaitBudget = originalBudget
		ingressGatePollInterval = originalInterval
	})
}

func TestConnectorRuntimeIgnoresEventWhenBackupPrepareOutlastsTheWaitBudget(t *testing.T) {
	shrinkIngressGateWaitBudget(t)
	connectorRuntime, taskRunService := newIngressGateTestRuntime(staticIngressGate{isPaused: true})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), &testAdapter{}, PlatformInboundEvent{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		SenderID:       "sender-1",
		ReplyTargetID:  "reply-target-1",
		Prompt:         "hello",
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Ignored || result.Reason != "backup_prepare_active" {
		t.Fatalf("expected backup prepare result, got %+v", result)
	}
	if len(taskRunService.ListTaskRun()) != 0 {
		t.Fatal("expected no task while backup prepare is active")
	}
}

func TestConnectorRuntimeWaitsOutABackupPrepareThatFinishesInTime(t *testing.T) {
	shrinkIngressGateWaitBudget(t)
	gate := &countdownIngressGate{}
	gate.pausedPollsRemaining.Store(3)
	connectorRuntime, _ := newIngressGateTestRuntime(gate)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), &testAdapter{}, PlatformInboundEvent{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		SenderID:       "sender-1",
		ReplyTargetID:  "reply-target-1",
		Prompt:         "hello",
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Ignored && result.Reason == "backup_prepare_active" {
		t.Fatalf("expected the event to proceed after the gate released, got %+v", result)
	}
}

type staticIngressGate struct {
	isPaused bool
}

func (staticIngressGate staticIngressGate) IsPaused() bool {
	return staticIngressGate.isPaused
}

type countdownIngressGate struct {
	pausedPollsRemaining atomic.Int64
}

func (gate *countdownIngressGate) IsPaused() bool {
	return gate.pausedPollsRemaining.Add(-1) >= 0
}
