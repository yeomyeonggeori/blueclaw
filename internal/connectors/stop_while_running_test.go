//go:build !nobundledharness

package connectors

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/defaultharness"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessdriver"
	"github.com/yeomyeonggeori/blueclaw/internal/harnessselection"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/reply"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const stopRequestPrompt = "그만해"

const slowToolName = "slow_lookup"

const promptCancellationLimit = 2 * time.Second

type blockedCall struct {
	started             chan struct{}
	cancelled           chan struct{}
	released            chan struct{}
	startOnce           sync.Once
	endOnce             sync.Once
	releaseOnce         sync.Once
	ignoresCancellation bool
}

func (call *blockedCall) release() {
	call.releaseOnce.Do(func() { close(call.released) })
}

func newBlockedCall() *blockedCall {
	return &blockedCall{started: make(chan struct{}), cancelled: make(chan struct{}), released: make(chan struct{})}
}

func (call *blockedCall) blockUntilCancelled(ctx context.Context) error {
	call.startOnce.Do(func() { close(call.started) })
	if call.ignoresCancellation {
		<-call.released
		return context.DeadlineExceeded
	}
	select {
	case <-ctx.Done():
		call.endOnce.Do(func() { close(call.cancelled) })
		return ctx.Err()
	case <-call.released:
		return context.DeadlineExceeded
	}
}

type blockingCapabilityService struct {
	call        *blockedCall
	invocations atomic.Int32
}

func (service *blockingCapabilityService) client() capability.Client {
	return capability.Client{Endpoint: "http://capability.test", HTTPClient: testHTTPDoer(service.answer)}
}

func (service *blockingCapabilityService) answer(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/v1/capabilities" {
		return testCapabilityRegistrySelfHealResponse(), nil
	}
	service.invocations.Add(1)
	return nil, service.call.blockUntilCancelled(request.Context())
}

type blockingActionModel struct {
	*agenttest.ScriptedLanguageModel
	blockedActionNumber int
	call                *blockedCall
	actionCalls         atomic.Int32
}

func (languageModel *blockingActionModel) TextChatCompleter() (llm.ChatCompleter, bool) {
	return blockingChatCompleter{languageModel: languageModel}, true
}

type blockingChatCompleter struct {
	languageModel *blockingActionModel
}

func (completer blockingChatCompleter) GenerateChatCompletion(ctx context.Context, request llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	languageModel := completer.languageModel
	if request.SchemaName == "bluecollar_agent_turn_action" && int(languageModel.actionCalls.Add(1)) == languageModel.blockedActionNumber {
		return llm.ChatCompletionResponse{}, languageModel.call.blockUntilCancelled(ctx)
	}
	return languageModel.ScriptedLanguageModel.ChatCompleter().GenerateChatCompletion(ctx, request)
}

func slowToolCallAction() string {
	return `{"action":"continue","toolName":"` + slowToolName + `","toolInput":{},"nextStepPlan":{"objective":"look the answer up","expectedTools":[],"expectedNextResults":["the lookup answers"],"doneCriteria":["the lookup answered"],"risk":"the lookup may be slow","workingSetReason":"the lookup is required"}}`
}

type stopWhileRunningFixture struct {
	connectorRuntime *ConnectorRuntime
	adapter          *testAdapter
	queue            *testConnectorQueueRepository
	languageModel    *blockingActionModel
	capability       *blockingCapabilityService
	call             *blockedCall
	gatewayDecider   *scriptedGatewayDecider
	heldDecision     *heldDecision
}

type runningTaskScript struct {
	blockedActionNumber int
	actions             []string
	busyRoute           inboundengagement.BusyRoute
	relatesToActiveTask bool
	ignoresCancellation bool
	heldDecisionNumber  int
}

type heldDecision struct {
	number  int32
	calls   atomic.Int32
	reached chan struct{}
	release chan struct{}
}

type holdingGatewayDecider struct {
	*scriptedGatewayDecider
	held *heldDecision
}

func (decider holdingGatewayDecider) Decide(ctx context.Context, facts inboundengagement.Facts, observe agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	if decider.held.calls.Add(1) == decider.held.number {
		close(decider.held.reached)
		select {
		case <-decider.held.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return decider.scriptedGatewayDecider.Decide(ctx, facts, observe)
}

func gatewayDeciderFor(script runningTaskScript, decider *scriptedGatewayDecider) (inboundengagement.Decider, *heldDecision) {
	if script.heldDecisionNumber == 0 {
		return decider, nil
	}
	held := &heldDecision{number: int32(script.heldDecisionNumber), reached: make(chan struct{}), release: make(chan struct{})}
	return holdingGatewayDecider{scriptedGatewayDecider: decider, held: held}, held
}

func toolBlockedScript() runningTaskScript {
	return runningTaskScript{actions: []string{slowToolCallAction(), slowToolCallAction(), connectorFinishMessage("끝났습니다")}, busyRoute: inboundengagement.BusyRouteCancel, relatesToActiveTask: true}
}

func modelBlockedScript() runningTaskScript {
	return runningTaskScript{blockedActionNumber: 1, actions: []string{slowToolCallAction(), connectorFinishMessage("끝났습니다")}, busyRoute: inboundengagement.BusyRouteCancel, relatesToActiveTask: true}
}

func newStopWhileRunningFixture(t *testing.T, script runningTaskScript) *stopWhileRunningFixture {
	t.Helper()
	call := newBlockedCall()
	call.ignoresCancellation = script.ignoresCancellation
	blockedActionNumber, actions := script.blockedActionNumber, script.actions
	languageModel := &blockingActionModel{
		ScriptedLanguageModel: agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
			ActionResponses: actions,
			ChatResponsesBySchema: map[string][]string{
				"blueclaw_reply": {"진행 중이던 작업을 멈췄습니다."},
			},
			DefaultResponsesBySchema: map[string]string{
				"bluecollar_turn_router": `{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"scripted test default","userFacingReply":""}`,
			},
		}),
		blockedActionNumber: blockedActionNumber,
		call:                call,
	}
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	gatewayDecider := &scriptedGatewayDecider{addressing: addressedToBot(), busyRoute: script.busyRoute, relatesToActiveTask: script.relatesToActiveTask}
	decider, held := gatewayDeciderFor(script, gatewayDecider)
	connectorRuntime, adapter := connectorRuntimeForHarness(t, bundledACPHarness(t, taskRunService, languageModel), decider, reply.NewGenerator(languageModel, nil), taskRunService, languageModel)
	t.Cleanup(call.release)
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", slowToolName})
	service := &blockingCapabilityService{call: call}
	connectorRuntime.UseTestCapabilityTools(service.client(), []string{slowToolName})
	queue := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(queue)
	return &stopWhileRunningFixture{connectorRuntime: connectorRuntime, adapter: adapter, queue: queue, languageModel: languageModel, capability: service, call: call, gatewayDecider: gatewayDecider, heldDecision: held}
}

func bundledACPHarness(t *testing.T, taskRunService *task.TaskRunService, languageModel llm.LanguageModelProvider) agentcontract.Harness {
	t.Helper()
	tokenCount := atomic.Int64{}
	resolver := mcpserver.NewSessionTokenRequesterResolver(func() string { return "session-" + strconv.FormatInt(tokenCount.Add(1), 10) })
	server := httptest.NewServer(mcpserver.NewToolCatalogHandler(resolver, "test"))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	factory, errorValue := harnessselection.Select(
		config.HarnessConfiguration{Name: harnessselection.BundledACPHarnessName},
		harnessselection.ToolCatalogEndpoint{URL: server.URL, Resolver: resolver},
		harnessselection.SandboxProcessBoundary{},
		harnessselection.WithBundledACPFactory(defaultharness.NewFactory),
	)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	harness, _ := factory(harnessdriver.Dependencies{
		TaskRunStore:                taskRunService,
		TaskTierLanguageModels:      agentcontract.TaskTierLanguageModels{XLow: languageModel, Low: languageModel, Medium: languageModel, High: languageModel, XHigh: languageModel, Max: languageModel},
		IntakeLanguageModelProvider: languageModel,
		DecisionModel:               &intaketest.LanguageModelDecisionModel{LanguageModel: languageModel},
	})
	return harness
}

func (fixture *stopWhileRunningFixture) send(t *testing.T, event PlatformInboundEvent) {
	t.Helper()
	if _, errorValue := fixture.connectorRuntime.HandleInboundEvent(context.Background(), fixture.adapter, event); errorValue != nil {
		t.Fatalf("the message could not be queued: %v", errorValue)
	}
}

func (fixture *stopWhileRunningFixture) startTheRequestInTheBackground(t *testing.T, event PlatformInboundEvent) <-chan struct{} {
	t.Helper()
	fixture.send(t, event)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}()
	select {
	case <-fixture.call.started:
	case <-finished:
		t.Fatal("the request finished before its blocked call started")
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the blocked call never started")
	}
	return finished
}

func (fixture *stopWhileRunningFixture) deliverAProgressNote(t *testing.T, request PlatformInboundEvent, runningTask task.TaskRun) {
	t.Helper()
	request.Platform = fixture.adapter.Name()
	replyTarget := ReplyTarget{ConversationID: request.ConversationID, ReplyTargetID: request.ReplyTargetID}
	note := OutboundReply{Message: "터미널을 여는 중입니다.", TaskRunID: runningTask.TaskRunID, ReplyKind: connectorReplyKindCheckpoint}
	if _, errorValue := fixture.connectorRuntime.enqueueConnectorReply(withConnectorEvent(context.Background(), request), replyTarget, note); errorValue != nil {
		t.Fatalf("the progress note could not be queued: %v", errorValue)
	}
	if !fixture.connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
		t.Fatal("the outbox delivered nothing")
	}
}

func (fixture *stopWhileRunningFixture) runningTask(t *testing.T) task.TaskRun {
	t.Helper()
	for _, taskRun := range fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1") {
		if taskRun.Status == task.TaskStatusRunning {
			return taskRun
		}
	}
	t.Fatalf("no task is running, runs: %+v", fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
	return task.TaskRun{}
}

func stopRequestEvent() PlatformInboundEvent {
	event := testInboundEvent("message-stop-request")
	event.Prompt = stopRequestPrompt
	event.RawReceivedAt = time.Now()
	return event
}

func longRequestEvent() PlatformInboundEvent {
	event := testInboundEvent("message-long-request")
	event.Prompt = "터미널로 새 창 띄워서 만들면 되잖아."
	event.RawReceivedAt = time.Now()
	return event
}

func (fixture *stopWhileRunningFixture) assertTheStopReachedTheRunningTask(t *testing.T, runningTask task.TaskRun, settled <-chan struct{}) {
	t.Helper()
	select {
	case <-fixture.call.cancelled:
	case <-time.After(promptCancellationLimit):
		t.Fatalf("the blocked call was still running %s after the stop arrived; the stop was released %d times to the conversation in flight; ledger: %s", promptCancellationLimit, fixture.releasedCount(), ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
	}
	select {
	case <-settled:
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the stop request was never settled")
	}
	if !connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, agentcontract.TaskEventTaskCancelRequested, "message-stop-request") {
		t.Fatalf("the running task's ledger records no cancel request: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
	}
	cancelledTask, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(runningTask.TaskRunID)
	if cancelledTask.Status != task.TaskStatusCancelled {
		t.Fatalf("the running task was not cancelled, status %q", cancelledTask.Status)
	}
	if !fixture.queueHoldsAReplyFor(runningTask.TaskRunID, "진행 중이던 작업을 멈췄습니다.") {
		t.Fatalf("the person got no cancel reply, replies: %+v", fixture.queue.pendingReplies)
	}
	if runs := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"); len(runs) != 1 {
		t.Fatalf("the stop started a run of its own: %+v", runs)
	}
}

func (fixture *stopWhileRunningFixture) queueHoldsAReplyFor(taskRunID string, message string) bool {
	fixture.queue.mutex.Lock()
	defer fixture.queue.mutex.Unlock()
	for _, queuedReply := range fixture.queue.pendingReplies {
		if queuedReply.Reply.TaskRunID == taskRunID && queuedReply.Reply.Message == message {
			return true
		}
	}
	return false
}

func (fixture *stopWhileRunningFixture) queueHoldsTheRequestsOwnReply(taskRunID string) bool {
	fixture.queue.mutex.Lock()
	defer fixture.queue.mutex.Unlock()
	for _, queuedReply := range fixture.queue.pendingReplies {
		if queuedReply.Reply.TaskRunID == taskRunID && queuedReply.RawEventID == longRequestEvent().DedupeKey() && queuedReply.Reply.ReplyKind != connectorReplyKindCheckpoint {
			return true
		}
	}
	return false
}

func ledgerNames(connectorRuntime *ConnectorRuntime, taskRunID string) string {
	names := []string{}
	for _, taskEvent := range connectorRuntime.taskRunService.ListTaskEvent(taskRunID) {
		names = append(names, taskEvent.Name)
	}
	return strings.Join(names, ", ")
}

func (fixture *stopWhileRunningFixture) settleTheStop(t *testing.T) <-chan struct{} {
	t.Helper()
	fixture.send(t, stopRequestEvent())
	settled := make(chan struct{})
	go func() {
		defer close(settled)
		fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}()
	return settled
}

func (fixture *stopWhileRunningFixture) releasedCount() int {
	fixture.queue.mutex.Lock()
	defer fixture.queue.mutex.Unlock()
	return len(fixture.queue.releasedEvents)
}

func (fixture *stopWhileRunningFixture) assertNothingStartedAfterTheStop(t *testing.T, requestFinished <-chan struct{}, actionCallsAtStop int32, toolCallsAtStop int32) {
	t.Helper()
	select {
	case <-requestFinished:
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the cancelled request never finished")
	}
	if calls := fixture.languageModel.actionCalls.Load(); calls != actionCallsAtStop {
		t.Fatalf("the loop asked the model for %d more steps after the stop", calls-actionCallsAtStop)
	}
	if calls := fixture.capability.invocations.Load(); calls != toolCallsAtStop {
		t.Fatalf("the loop made %d more tool calls after the stop", calls-toolCallsAtStop)
	}
}

func (fixture *stopWhileRunningFixture) startTheAnsweredRequest(t *testing.T) (task.TaskRun, <-chan struct{}) {
	t.Helper()
	request := longRequestEvent()
	requestFinished := fixture.startTheRequestInTheBackground(t, request)
	runningTask := fixture.runningTask(t)
	fixture.deliverAProgressNote(t, request, runningTask)
	return runningTask, requestFinished
}

func (fixture *stopWhileRunningFixture) callCounts() (int32, int32) {
	return fixture.languageModel.actionCalls.Load(), fixture.capability.invocations.Load()
}

type startRequest func(*stopWhileRunningFixture, *testing.T) (task.TaskRun, <-chan struct{})

func requestShapes() map[string]startRequest {
	return map[string]startRequest{
		"after a progress note": (*stopWhileRunningFixture).startTheAnsweredRequest,
		"before any reply":      (*stopWhileRunningFixture).startTheUnansweredRequest,
	}
}

func (fixture *stopWhileRunningFixture) startTheUnansweredRequest(t *testing.T) (task.TaskRun, <-chan struct{}) {
	t.Helper()
	requestFinished := fixture.startTheRequestInTheBackground(t, longRequestEvent())
	return fixture.runningTask(t), requestFinished
}

func eachBlockedCallAndRequestShape(t *testing.T, test func(*testing.T, runningTaskScript, startRequest)) {
	for scriptName, script := range map[string]runningTaskScript{"a tool call is blocked": toolBlockedScript(), "a model call is blocked": modelBlockedScript()} {
		for shapeName, start := range requestShapes() {
			t.Run(scriptName+" "+shapeName, func(t *testing.T) { test(t, script, start) })
		}
	}
}

func eachRequestShape(t *testing.T, test func(*testing.T, startRequest)) {
	for shapeName, start := range requestShapes() {
		t.Run(shapeName, func(t *testing.T) { test(t, start) })
	}
}

func TestAStopSentWhileTheTaskRunsCancelsItAtOnce(t *testing.T) {
	eachBlockedCallAndRequestShape(t, func(t *testing.T, script runningTaskScript, start startRequest) {
		fixture := newStopWhileRunningFixture(t, script)
		runningTask, requestFinished := start(fixture, t)
		actionCallsAtStop, toolCallsAtStop := fixture.callCounts()

		settled := fixture.settleTheStop(t)

		fixture.assertTheStopReachedTheRunningTask(t, runningTask, settled)
		fixture.assertNothingStartedAfterTheStop(t, requestFinished, actionCallsAtStop, toolCallsAtStop)
		fixture.assertNothingWasReadTogether(t, runningTask)
		if connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, task.TaskEventLaunchFailureReply, "") {
			t.Fatalf("a failure notice was written for a run the person stopped: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
		}
	})
}

func TestARootStopInADirectMessageCancelsTheTaskARootMessageStarted(t *testing.T) {
	fixture := newStopWhileRunningFixture(t, toolBlockedScript())
	requestFinished := fixture.startTheRequestInTheBackground(t, atRoot(longRequestEvent()))
	runningTask := fixture.runningTask(t)
	actionCallsAtStop, toolCallsAtStop := fixture.callCounts()

	fixture.send(t, atRoot(stopRequestEvent()))

	fixture.assertTheStopReachedTheRunningTask(t, runningTask, fixture.processTheNextMessage())
	fixture.assertNothingStartedAfterTheStop(t, requestFinished, actionCallsAtStop, toolCallsAtStop)
	fixture.assertNothingWasReadTogether(t, runningTask)
}

func atRoot(event PlatformInboundEvent) PlatformInboundEvent {
	isThread := false
	event.ReplyTargetID = event.MessageID
	event.IsThread = &isThread
	return event
}

func TestTheCancelReplyDoesNotWaitForTheInterruptedCallToUnwind(t *testing.T) {
	eachRequestShape(t, func(t *testing.T, start startRequest) {
		script := toolBlockedScript()
		script.ignoresCancellation = true
		fixture := newStopWhileRunningFixture(t, script)
		runningTask, requestFinished := start(fixture, t)

		settled := fixture.settleTheStop(t)

		select {
		case <-settled:
		case <-requestFinished:
			t.Fatal("the request finished while its tool call was still blocked")
		case <-agenttest.WhenTheTestRunsOut(t):
			t.Fatalf("the stop waited for the blocked call; ledger: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
		}
		if !fixture.queueHoldsAReplyFor(runningTask.TaskRunID, "진행 중이던 작업을 멈췄습니다.") {
			t.Fatalf("the person got no cancel reply while the call was blocked, replies: %+v", fixture.queue.pendingReplies)
		}
		if !connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, agentcontract.ToolTaskEventName(slowToolName, agentcontract.ToolTaskEventCancelledSuffix), "") {
			t.Fatalf("the ledger does not say the call in flight was cancelled: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
		}
	})
}

func TestAnIndependentMessageLeavesTheRunningTaskAlone(t *testing.T) {
	eachRequestShape(t, func(t *testing.T, start startRequest) {
		script := toolBlockedScript()
		script.busyRoute = inboundengagement.BusyRouteUnrelated
		script.relatesToActiveTask = false
		fixture := newStopWhileRunningFixture(t, script)
		runningTask, requestFinished := start(fixture, t)

		settled := fixture.settleTheStop(t)

		awaitSettled(t, settled)
		assertTheTaskIsStillRunning(t, fixture, runningTask)
		if fixture.releasedCount() != 1 {
			t.Fatalf("an independent message waits for the conversation in flight, released %d times", fixture.releasedCount())
		}
		fixture.letTheRunFinish(t, requestFinished)
		fixture.drainTheInbox(t)
		if !fixture.queueHoldsTheRequestsOwnReply(runningTask.TaskRunID) {
			t.Fatalf("the running task's own reply was not kept, replies: %+v", fixture.queue.pendingReplies)
		}
		fixture.assertTheMessageRanAsItsOwnRequest(t, runningTask, stopRequestPrompt)
	})
}

func TestACorrectionWhileTheTaskRunsSteersIt(t *testing.T) {
	eachRequestShape(t, func(t *testing.T, start startRequest) {
		script := toolBlockedScript()
		script.busyRoute = inboundengagement.BusyRouteSteer
		fixture := newStopWhileRunningFixture(t, script)
		runningTask, _ := start(fixture, t)

		settled := fixture.settleTheStop(t)

		awaitSettled(t, settled)
		assertTheTaskIsStillRunning(t, fixture, runningTask)
		if !connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, agentcontract.TaskEventTaskSteerRequested, stopRequestPrompt) {
			t.Fatalf("the correction was not steered into the running task: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
		}
		if runs := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"); len(runs) != 1 {
			t.Fatalf("the correction started a run of its own: %+v", runs)
		}
		fixture.assertNothingWasReadTogether(t, runningTask)
	})
}

func TestTwoMessagesSentBackToBackWhileTheTaskRunsAreEachDecided(t *testing.T) {
	eachRequestShape(t, func(t *testing.T, start startRequest) {
		script := toolBlockedScript()
		script.busyRoute = inboundengagement.BusyRouteSteer
		fixture := newStopWhileRunningFixture(t, script)
		runningTask, _ := start(fixture, t)
		fixture.send(t, correctionEvent("message-first-correction", "HTML로 해줘"))

		settled := fixture.settleTheStop(t)

		awaitSettled(t, settled)
		fixture.drainTheInbox(t)
		assertTheTaskIsStillRunning(t, fixture, runningTask)
		for _, correction := range []string{"HTML로 해줘", stopRequestPrompt} {
			if !connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, agentcontract.TaskEventTaskSteerRequested, correction) {
				t.Fatalf("%q was not steered into the running task: %s", correction, ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
			}
		}
		if runs := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"); len(runs) != 1 {
			t.Fatalf("the corrections started a run of their own: %+v", runs)
		}
	})
}

func TestAStopWhoseDecisionFailsWaitsBehindTheRunUntilItCanBeDecided(t *testing.T) {
	fixture := newStopWhileRunningFixture(t, toolBlockedScript())
	runningTask, requestFinished := fixture.startTheUnansweredRequest(t)
	fixture.gatewayDecider.failWith(errors.New("the decision model is unreachable"))

	awaitSettled(t, fixture.settleTheStop(t))

	assertTheTaskIsStillRunning(t, fixture, runningTask)
	if fixture.releasedCount() != 1 {
		t.Fatalf("an undecided stop is not held behind the running task, released %d times", fixture.releasedCount())
	}
	fixture.gatewayDecider.failWith(nil)
	fixture.assertTheStopReachedTheRunningTask(t, runningTask, fixture.processTheNextMessage())
	fixture.assertNothingStartedAfterTheStop(t, requestFinished, fixture.languageModel.actionCalls.Load(), fixture.capability.invocations.Load())
}

func TestAStopDecidedAfterTheTaskFinishedOnItsOwnIsToldItFinished(t *testing.T) {
	script := toolBlockedScript()
	script.heldDecisionNumber = 2
	fixture := newStopWhileRunningFixture(t, script)
	runningTask, requestFinished := fixture.startTheUnansweredRequest(t)

	settled := fixture.settleTheStop(t)
	awaitHeldDecision(t, fixture.heldDecision)
	fixture.letTheRunFinish(t, requestFinished)
	close(fixture.heldDecision.release)

	awaitSettled(t, settled)
	finishedTask, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(runningTask.TaskRunID)
	if isTaskControlActiveStatus(finishedTask.Status) || finishedTask.Status == task.TaskStatusCancelled {
		t.Fatalf("the task that finished on its own reads %q", finishedTask.Status)
	}
	if !connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, agentcontract.TaskEventTaskBusyMessageAfterFinish, "message-stop-request") {
		t.Fatalf("the stop was not answered as arriving after the task finished: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
	}
	if !fixture.queueHoldsTheRequestsOwnReply(runningTask.TaskRunID) {
		t.Fatalf("the finished task's own reply was dropped, replies: %+v", fixture.queue.pendingReplies)
	}
	if runs := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"); len(runs) != 1 {
		t.Fatalf("the late stop started a run of its own: %+v", runs)
	}
}

func TestMessagesSentBeforeTheRunStartsAreReadAsOneRequest(t *testing.T) {
	fixture := newStopWhileRunningFixture(t, toolBlockedScript())
	fixture.send(t, longRequestEvent())
	fixture.send(t, correctionEvent("message-correction", "PDF 말고 HTML로"))

	fixture.processTheNextMessage()

	fixture.assertOneRunReadBoth(t)
}

func TestAMessageSentWhileTheRequestIsDecidedIsReadWithIt(t *testing.T) {
	script := toolBlockedScript()
	script.heldDecisionNumber = 1
	fixture := newStopWhileRunningFixture(t, script)
	fixture.send(t, longRequestEvent())
	firstSettled := fixture.processTheNextMessage()
	awaitHeldDecision(t, fixture.heldDecision)

	fixture.send(t, correctionEvent("message-correction", "PDF 말고 HTML로"))
	awaitSettled(t, firstSettled)
	fixture.processTheNextMessage()

	fixture.assertOneRunReadBoth(t)
}

func TestAStopLeavesNothingALaterRequestIsReadWith(t *testing.T) {
	fixture := newStopWhileRunningFixture(t, toolBlockedScript())
	runningTask, requestFinished := fixture.startTheUnansweredRequest(t)
	fixture.assertTheStopReachedTheRunningTask(t, runningTask, fixture.settleTheStop(t))
	awaitSettled(t, requestFinished)
	for fixture.connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
	}
	fixture.call.release()

	fixture.send(t, correctionEvent("message-later-request", "회의록 정리해줘"))
	fixture.drainTheInbox(t)

	fixture.assertTheMessageRanAsItsOwnRequest(t, runningTask, "회의록 정리해줘")
}

func correctionEvent(messageID string, prompt string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.Prompt = prompt
	event.RawReceivedAt = time.Now()
	return event
}

func (decider *scriptedGatewayDecider) failWith(errorValue error) {
	decider.mutex.Lock()
	defer decider.mutex.Unlock()
	decider.errorValue = errorValue
}

func awaitHeldDecision(t *testing.T, held *heldDecision) {
	t.Helper()
	select {
	case <-held.reached:
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the decision to hold was never asked")
	}
}

func (fixture *stopWhileRunningFixture) processTheNextMessage() <-chan struct{} {
	settled := make(chan struct{})
	go func() {
		defer close(settled)
		fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}()
	return settled
}

func (fixture *stopWhileRunningFixture) letTheRunFinish(t *testing.T, requestFinished <-chan struct{}) {
	t.Helper()
	fixture.call.release()
	select {
	case <-requestFinished:
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the running task never finished on its own")
	}
}

func (fixture *stopWhileRunningFixture) drainTheInbox(t *testing.T) {
	t.Helper()
	for attempt := 0; fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background()); attempt++ {
		if attempt > 10 {
			t.Fatal("the inbox never drained")
		}
	}
}

func (fixture *stopWhileRunningFixture) assertNothingWasReadTogether(t *testing.T, runningTask task.TaskRun) {
	t.Helper()
	if connectorTaskEventsContain(fixture.connectorRuntime, runningTask.TaskRunID, agentcontract.TaskEventTaskSupersededByMessage, "") {
		t.Fatalf("the running task was superseded instead of decided about: %s", ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
	}
	fixture.drainTheInbox(t)
	if runs := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"); len(runs) != 1 {
		t.Fatalf("the message was read together with the running request into a run of its own: %+v", runs)
	}
}

func (fixture *stopWhileRunningFixture) assertTheMessageRanAsItsOwnRequest(t *testing.T, earlierTask task.TaskRun, prompt string) {
	t.Helper()
	for _, taskRun := range fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1") {
		if taskRun.TaskRunID == earlierTask.TaskRunID {
			continue
		}
		if taskRun.Prompt != prompt {
			t.Fatalf("the message ran as %q, read together with the earlier request", taskRun.Prompt)
		}
		return
	}
	t.Fatalf("the message never ran as a request of its own, runs: %+v", fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
}

func (fixture *stopWhileRunningFixture) assertOneRunReadBoth(t *testing.T) {
	t.Helper()
	select {
	case <-fixture.call.started:
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the request read together never ran")
	}
	runs := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")
	if len(runs) != 1 {
		t.Fatalf("messages sent before the run started ran apart: %+v", runs)
	}
	for _, prompt := range []string{longRequestEvent().Prompt, "PDF 말고 HTML로"} {
		if !strings.Contains(runs[0].Prompt, prompt) {
			t.Fatalf("the one run did not read %q: %q", prompt, runs[0].Prompt)
		}
	}
}

func awaitSettled(t *testing.T, settled <-chan struct{}) {
	t.Helper()
	select {
	case <-settled:
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the message was never settled")
	}
}

func assertTheTaskIsStillRunning(t *testing.T, fixture *stopWhileRunningFixture, runningTask task.TaskRun) {
	t.Helper()
	currentTask, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(runningTask.TaskRunID)
	if currentTask.Status != task.TaskStatusRunning {
		t.Fatalf("the running task was disturbed, status %q; ledger: %s", currentTask.Status, ledgerNames(fixture.connectorRuntime, runningTask.TaskRunID))
	}
	select {
	case <-fixture.call.cancelled:
		t.Fatal("the running task's call in flight was cancelled")
	default:
	}
}
