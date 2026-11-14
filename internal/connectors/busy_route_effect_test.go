//go:build !nobundledharness

package connectors

import (
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

const messageSentWhileTheTaskRuns = "회의록 정리해줘"

var routesThatRunTheMessageAsItsOwnTask = []inboundengagement.BusyRoute{inboundengagement.BusyRouteReplace, inboundengagement.BusyRouteNewTask}

func TestEachBusyRouteDoesToTheRunningTaskWhatItDeclares(t *testing.T) {
	for _, routeName := range inboundengagement.BusyRouteNames {
		route := inboundengagement.BusyRoute(routeName)
		t.Run(routeName, func(t *testing.T) {
			script := toolBlockedScript()
			script.busyRoute = route
			fixture := newStopWhileRunningFixture(t, script)
			runningTask, requestFinished := fixture.startTheUnansweredRequest(t)
			fixture.send(t, correctionEvent("message-while-running", messageSentWhileTheTaskRuns))

			settled := fixture.processTheNextMessage()

			fixture.awaitTheRoutesEffect(t, route, runningTask, settled)
			fixture.letEverythingFinish(t, requestFinished, settled)
			fixture.assertTheRunningTaskEndedAsDeclared(t, route, runningTask)
			fixture.assertTheMessageRanAsDeclared(t, route, runningTask)
		})
	}
}

func (fixture *stopWhileRunningFixture) awaitTheRoutesEffect(t *testing.T, route inboundengagement.BusyRoute, runningTask task.TaskRun, settled <-chan struct{}) {
	t.Helper()
	if route.EndsTheRunningTask() {
		select {
		case <-fixture.call.cancelled:
		case <-agenttest.WhenTheTestRunsOut(t):
			t.Fatalf("%s did not end the running task: %s", route, ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
		}
		return
	}
	select {
	case <-settled:
	case <-time.After(promptCancellationLimit):
		t.Fatalf("%s was still being handled %s later, beside the running task: %+v", route, promptCancellationLimit, fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
	}
	assertTheTaskIsStillRunning(t, fixture, runningTask)
	if route.WaitsForTheRunningTask() && fixture.releasedCount() == 0 {
		t.Fatalf("%s did not wait behind the running task", route)
	}
}

func (fixture *stopWhileRunningFixture) letEverythingFinish(t *testing.T, requestFinished <-chan struct{}, settled <-chan struct{}) {
	t.Helper()
	fixture.letTheRunFinish(t, requestFinished)
	awaitSettled(t, settled)
	fixture.drainTheInbox(t)
}

func (fixture *stopWhileRunningFixture) assertTheRunningTaskEndedAsDeclared(t *testing.T, route inboundengagement.BusyRoute, runningTask task.TaskRun) {
	t.Helper()
	endedTask, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(runningTask.TaskRunID)
	if isCancelled := endedTask.Status == task.TaskStatusCancelled; isCancelled != route.EndsTheRunningTask() {
		t.Fatalf("%s left the running task %q; it declares EndsTheRunningTask %v", route, endedTask.Status, route.EndsTheRunningTask())
	}
}

func (fixture *stopWhileRunningFixture) assertTheMessageRanAsDeclared(t *testing.T, route inboundengagement.BusyRoute, runningTask task.TaskRun) {
	t.Helper()
	runs := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")
	for _, ownTaskRoute := range routesThatRunTheMessageAsItsOwnTask {
		if route == ownTaskRoute {
			fixture.assertTheMessageRanAsItsOwnRequest(t, runningTask, messageSentWhileTheTaskRuns)
			return
		}
	}
	if len(runs) != 1 {
		t.Fatalf("%s started a run of its own: %+v", route, runs)
	}
}
