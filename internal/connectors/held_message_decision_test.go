//go:build !nobundledharness

package connectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const reclaimsDuringTheRun = 20

func TestAMessageHeldBehindTheRunIsDecidedOnceHoweverOftenItIsReclaimed(t *testing.T) {
	script := toolBlockedScript()
	script.busyRoute = inboundengagement.BusyRouteNewTask
	script.relatesToActiveTask = false
	fixture := newStopWhileRunningFixture(t, script)
	runningTask, requestFinished := fixture.startTheUnansweredRequest(t)
	decisionsBefore := fixture.gatewayDecider.calls()
	fixture.send(t, correctionEvent("message-independent-request", "회의록 정리해줘"))

	fixture.reclaimTheHeldMessage(t, reclaimsDuringTheRun)

	if decisions := fixture.gatewayDecider.calls() - decisionsBefore; decisions != 1 {
		t.Fatalf("a message held behind the run was decided %d times over %d reclaims", decisions, reclaimsDuringTheRun)
	}
	assertTheTaskIsStillRunning(t, fixture, runningTask)
	fixture.letTheRunFinish(t, requestFinished)
	fixture.drainTheInbox(t)
	fixture.assertTheMessageRanAsItsOwnRequest(t, runningTask, "회의록 정리해줘")
	if decisions := fixture.gatewayDecider.calls() - decisionsBefore; decisions != 1 {
		t.Fatalf("the message was decided again about the run it waited behind, %d decisions in all", decisions)
	}
}

func TestTwoMessagesHeldBehindTheRunAreNotDecidedAgainAsABurst(t *testing.T) {
	script := toolBlockedScript()
	script.relatesToActiveTask = false
	fixture := newStopWhileRunningFixture(t, script)
	runningTask, _ := fixture.startTheUnansweredRequest(t)
	decisionsBefore := fixture.gatewayDecider.calls()
	fixture.send(t, correctionEvent("message-first-request", "회의록 정리해줘"))
	fixture.send(t, correctionEvent("message-second-request", "내일 일정 알려줘"))

	fixture.reclaimTheHeldMessage(t, reclaimsDuringTheRun)

	if decisions := fixture.gatewayDecider.calls() - decisionsBefore; decisions != 1 {
		t.Fatalf("two messages held behind the run were decided %d times over %d reclaims", decisions, reclaimsDuringTheRun)
	}
	assertTheTaskIsStillRunning(t, fixture, runningTask)
}

func TestAFailingDecisionAboutTheRunningTaskBacksOffAndStops(t *testing.T) {
	fixture := newStopWhileRunningFixture(t, toolBlockedScript())
	runningTask, requestFinished := fixture.startTheUnansweredRequest(t)
	fixture.gatewayDecider.failWith(errors.New("the decision model is unreachable"))
	decisionsBefore := fixture.gatewayDecider.calls()
	fixture.send(t, stopRequestEvent())

	fixture.reclaimTheHeldMessage(t, reclaimsDuringTheRun)

	if decisions := fixture.gatewayDecider.calls() - decisionsBefore; decisions != connectorMaximumAttemptCount {
		t.Fatalf("a failing decision was asked %d times over %d reclaims, want %d", decisions, reclaimsDuringTheRun, connectorMaximumAttemptCount)
	}
	fixture.assertTheRetriesBackedOff(t)
	assertTheTaskIsStillRunning(t, fixture, runningTask)
	fixture.gatewayDecider.failWith(nil)
	fixture.letTheRunFinish(t, requestFinished)
	fixture.drainTheInbox(t)
	if !connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, agentcontract.TaskEventTaskBusyMessageAfterFinish, "message-stop-request") {
		t.Fatalf("the undecided stop was not answered once the run ended: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
	}
}

func (fixture *stopWhileRunningFixture) reclaimTheHeldMessage(t *testing.T, times int) {
	t.Helper()
	for range times {
		if !fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background()) {
			t.Fatal("no message was held behind the run")
		}
	}
}

func (fixture *stopWhileRunningFixture) assertTheRetriesBackedOff(t *testing.T) {
	t.Helper()
	fixture.queue.mutex.Lock()
	delays := append([]time.Duration{}, fixture.queue.releaseDelays...)
	fixture.queue.mutex.Unlock()
	for index := 1; index < connectorMaximumAttemptCount-1; index++ {
		if delays[index] <= delays[index-1] {
			t.Fatalf("retry %d waited %s after %s, the decision did not back off: %v", index+1, delays[index], delays[index-1], delays)
		}
	}
}
