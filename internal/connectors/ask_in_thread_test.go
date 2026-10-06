package connectors

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

const threadAskRequest = "내일 휴가 일정을 캘린더에서 삭제해줘"

type threadAskFixture struct {
	connectorRuntime *ConnectorRuntime
	adapter          *testAdapter
	through          PlatformAdapter
	mutex            sync.Mutex
	invokedTools     []string
}

func (fixture *threadAskFixture) recordInvokedTool(toolName string) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	fixture.invokedTools = append(fixture.invokedTools, toolName)
}

func (fixture *threadAskFixture) invokedToolCount() int {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	return len(fixture.invokedTools)
}

type threadAskScript struct {
	approvalReplies []string
	turnRouters     []string
	actions         []string
}

func startTaskRoute(reason string) string {
	return `{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"` + reason + `","userFacingReply":""}`
}

func answerQuestionRoute(reason string) string {
	return `{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","requestedOutputFormats":null,"responseLanguage":"ko","reason":"` + reason + `","userFacingReply":"","approval":"unclear"}`
}

func newThreadAskFixture(t *testing.T, script threadAskScript) *threadAskFixture {
	t.Helper()
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ChatResponsesBySchema: map[string][]string{
			"blueclaw_reply": {"그 일정은 내일 휴가입니다."},
		},
		StructuredResponsesBySchema: map[string][]string{
			"blueclaw_approval_reply": script.approvalReplies,
			"bluecollar_turn_router":  script.turnRouters,
			"bluecollar_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
			},
			"approval_question": {`{"question":"내일 휴가 일정을 삭제할까요?"}`},
		},
		ActionResponses: script.actions,
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	fixture := &threadAskFixture{connectorRuntime: connectorRuntime, adapter: adapter, through: adapter}
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "ask_confirm", "event_delete"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/capabilities" {
				return testCapabilityRegistrySelfHealResponse(), nil
			}
			fixture.recordInvokedTool(strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
		}),
	}, []string{"event_delete"})
	connectorRuntime.approvalGate.UsePermissionAsker(connectorRuntime.ThreadPermissionAsker())
	return fixture
}

type handledEvent struct {
	result ConnectorRuntimeResult
	err    error
}

func (fixture *threadAskFixture) send(ctx context.Context, event PlatformInboundEvent) <-chan handledEvent {
	handled := make(chan handledEvent, 1)
	go func() {
		result, errorValue := fixture.connectorRuntime.HandleInboundEvent(ctx, fixture.through, event)
		handled <- handledEvent{result: result, err: errorValue}
	}()
	return handled
}

func (fixture *threadAskFixture) await(t *testing.T, handled <-chan handledEvent) ConnectorRuntimeResult {
	t.Helper()
	select {
	case outcome := <-handled:
		if outcome.err != nil {
			t.Fatalf("the event failed: %v", outcome.err)
		}
		return outcome.result
	case <-time.After(10 * time.Second):
		t.Fatal("the event was still being handled after ten seconds")
		return ConnectorRuntimeResult{}
	}
}

func (fixture *threadAskFixture) awaitQuestionOnTheThread(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, taskRun := range fixture.connectorRuntime.taskRunService.ListTaskRun() {
			if fixture.connectorRuntime.isAwaitedInThread(taskRun.TaskRunID) && PostedApprovalQuestionMessageID(fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)) != "" {
				return taskRun.TaskRunID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the run never asked its question on the thread")
	return ""
}

func (fixture *threadAskFixture) questionsPosted() int {
	count := 0
	for _, reply := range fixture.adapter.sentReplies {
		if reply.replyKind == connectorReplyKindApprovalQuestion {
			count++
		}
	}
	return count
}

func (fixture *threadAskFixture) taskRunCount() int {
	return len(fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
}

func threadReplyEvent(messageID string, prompt string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.Prompt = prompt
	return event
}

func rootReplyEvent(messageID string, prompt string) PlatformInboundEvent {
	event := threadReplyEvent(messageID, prompt)
	event.ReplyTargetID = messageID
	return event
}

func deleteApprovalScript(approvalReplies ...string) threadAskScript {
	return threadAskScript{
		approvalReplies: approvalReplies,
		turnRouters:     []string{startTaskRoute("calendar delete needs approval first"), startTaskRoute("calendar delete needs approval first")},
		actions: []string{
			`{"action":"continue","toolName":"event_delete","toolInput":{"eventHint":"event-1"}}`,
			connectorFinishMessageCiting("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-001"),
		},
	}
}

func TestAReplyInTheQuestionsThreadApprovesTheCallInPlace(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript(`{"answer":"approve"}`))

	asking := fixture.send(context.Background(), threadReplyEvent("message-1", threadAskRequest))
	taskRunID := fixture.awaitQuestionOnTheThread(t)
	answer := fixture.await(t, fixture.send(context.Background(), threadReplyEvent("message-2", "ㅇ")))
	result := fixture.await(t, asking)

	if answer.Reason != ApprovalAnsweredInThreadReason || answer.TaskRunID != taskRunID {
		t.Fatalf("the reply settled %+v, expected the waiting run's question", answer)
	}
	if result.TaskRunID != taskRunID || fixture.taskRunCount() != 1 {
		t.Fatalf("the run that asked finished as %+v among %d runs, expected it alone", result, fixture.taskRunCount())
	}
	taskRun, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if taskRun.Status != task.TaskStatusCompleted || fixture.invokedToolCount() != 1 || fixture.questionsPosted() != 1 {
		t.Fatalf("status %s, calls %v, questions %d: expected a completed run that called once after one question", taskRun.Status, fixture.invokedTools, fixture.questionsPosted())
	}
}

func TestARootReplyToAThreadQuestionIsNotAnAnswer(t *testing.T) {
	script := deleteApprovalScript()
	script.turnRouters = append(script.turnRouters, answerQuestionRoute("a question beside the waiting call"), answerQuestionRoute("a question beside the waiting call"))
	fixture := newThreadAskFixture(t, script)
	askingContext, stopAsking := context.WithCancel(context.Background())
	asking := fixture.send(askingContext, threadReplyEvent("message-1", threadAskRequest))
	taskRunID := fixture.awaitQuestionOnTheThread(t)

	rootResult := fixture.await(t, fixture.send(context.Background(), rootReplyEvent("message-2", "ㅇ")))

	if rootResult.Reason == ApprovalAnsweredInThreadReason {
		t.Fatal("a reply outside the question's thread answered it")
	}
	if !fixture.connectorRuntime.isAwaitedInThread(taskRunID) || fixture.invokedToolCount() != 0 {
		t.Fatalf("the question was settled by a root reply: awaited=%v calls=%v", fixture.connectorRuntime.isAwaitedInThread(taskRunID), fixture.invokedTools)
	}
	stopAsking()
	fixture.await(t, asking)
}

func TestAReplyAfterAnotherExchangeStillContinuesTheRunThatAsked(t *testing.T) {
	script := deleteApprovalScript(`{"answer":"other"}`, `{"answer":"approve"}`)
	script.turnRouters = append(script.turnRouters, answerQuestionRoute("a question beside the waiting call"), answerQuestionRoute("a question beside the waiting call"))
	script.actions = []string{
		script.actions[0],
		connectorFinishMessage("내일 휴가로 등록된 일정 하나입니다."),
		script.actions[1],
	}
	fixture := newThreadAskFixture(t, script)
	asking := fixture.send(context.Background(), threadReplyEvent("message-1", threadAskRequest))
	taskRunID := fixture.awaitQuestionOnTheThread(t)

	between := fixture.await(t, fixture.send(context.Background(), threadReplyEvent("message-2", "그게 어떤 일정이었지?")))
	if between.Reason == ApprovalAnsweredInThreadReason || between.TaskRunID == taskRunID {
		t.Fatalf("a question about the call ran as %+v, expected its own turn", between)
	}
	answer := fixture.await(t, fixture.send(context.Background(), threadReplyEvent("message-3", "ㅇㅇ")))
	result := fixture.await(t, asking)

	if answer.TaskRunID != taskRunID || result.TaskRunID != taskRunID {
		t.Fatalf("ㅇㅇ settled %+v and the run ended as %+v, expected both on the run that asked", answer, result)
	}
	taskRun, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if taskRun.Status != task.TaskStatusCompleted || fixture.invokedToolCount() != 1 {
		t.Fatalf("status %s with calls %v, expected one call on a completed run", taskRun.Status, fixture.invokedTools)
	}
}

func restartedRunHoldingTheCall(t *testing.T, fixture *threadAskFixture) task.TaskRun {
	t.Helper()
	taskRun := fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1", IsThread: true}, threadAskRequest)
	fixture.connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentTaskLaunched, `{"platform":"test","conversationID":"direct-1","replyTargetID":"reply-target-1","sourceReference":"test:direct-1:message-1"}`)
	holdPendingCall(t, fixture.connectorRuntime, taskRun.TaskRunID)
	taskRun, _ = fixture.connectorRuntime.taskRunService.FindTaskRun(taskRun.TaskRunID)
	return taskRun
}

func TestAPendingQuestionIsPostedOnceAndAwaitedAfterARestart(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := restartedRunHoldingTheCall(t, fixture)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	go fixture.connectorRuntime.reawaitHold(ctx, taskRun)
	fixture.awaitQuestionOnTheThread(t)
	fixture.connectorRuntime.reawaitHold(ctx, taskRun)

	if fixture.questionsPosted() != 1 {
		t.Fatalf("a hold whose question was never posted was asked %d times, expected once", fixture.questionsPosted())
	}
}

func TestAQuestionAlreadyPostedBeforeARestartIsAwaitedButNotPostedAgain(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := restartedRunHoldingTheCall(t, fixture)
	fixture.connectorRuntime.appendConnectorReplyEvent(taskRun.TaskRunID, agentcontract.TaskEventConnectorReplySent, map[string]string{"replyKind": connectorReplyKindApprovalQuestion, "dispatchID": "dispatch-before-restart"})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	go fixture.connectorRuntime.reawaitHold(ctx, taskRun)
	fixture.awaitQuestionOnTheThread(t)

	if fixture.questionsPosted() != 0 {
		t.Fatalf("a question posted before the restart was posted %d more times", fixture.questionsPosted())
	}
}

func TestAnAnswerAfterARestartApprovesTheHoldAndResumesTheRunWhichSpendsIt(t *testing.T) {
	script := deleteApprovalScript(`{"answer":"approve"}`)
	script.actions = []string{script.actions[0], connectorFinishMessageCiting("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-001")}
	fixture := newThreadAskFixture(t, script)
	fixture.connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	taskRun := restartedRunHoldingTheCall(t, fixture)
	fixture.connectorRuntime.appendConnectorReplyEvent(taskRun.TaskRunID, agentcontract.TaskEventConnectorReplySent, map[string]string{"replyKind": connectorReplyKindApprovalQuestion, "dispatchID": "dispatch-before-restart"})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	finished := make(chan struct{})
	go func() {
		fixture.connectorRuntime.reawaitHold(ctx, taskRun)
		close(finished)
	}()
	fixture.awaitQuestionOnTheThread(t)

	fixture.await(t, fixture.send(context.Background(), threadReplyEvent("message-2", "ㅇ")))
	awaitFinished(t, finished)

	resumed, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRun.TaskRunID)
	holds := holdrecord.Holds(fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(holds) != 1 || holds[0].State != holdrecord.StateSpent || fixture.invokedToolCount() != 1 || resumed.Status != task.TaskStatusCompleted {
		t.Fatalf("holds %+v, calls %v, status %s: expected the harness's repeated call to spend the approved hold once on a completed run", holds, fixture.invokedTools, resumed.Status)
	}
}

func TestARunWhoseThreadCannotBeReopenedAfterARestartIsEndedNotParked(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "schedule:morning"}, "scheduled run")
	holdPendingCall(t, fixture.connectorRuntime, taskRun.TaskRunID)
	taskRun, _ = fixture.connectorRuntime.taskRunService.FindTaskRun(taskRun.TaskRunID)

	fixture.connectorRuntime.reawaitHold(context.Background(), taskRun)

	ended, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRun.TaskRunID)
	events := fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	if ended.Status == task.TaskStatusWaitingApproval || !connectorTaskEventsContain(fixture.connectorRuntime, taskRun.TaskRunID, approvalgate.TaskEventApprovalUnreachable, "") || len(events) == 0 {
		t.Fatalf("a run nobody can be asked about was left %s", ended.Status)
	}
}

func holdPendingCall(t *testing.T, connectorRuntime *ConnectorRuntime, taskRunID string) {
	t.Helper()
	if _, errorValue := connectorRuntime.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatalf("the run would not wait for approval: %v", errorValue)
	}
	holdrecord.Open(connectorRuntime.taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: []byte(`{"eventHint":"event-1"}`), Confirmation: "내일 휴가 일정을 삭제할까요?"}, nil)
}

func TestAWaitingRunLeavesTheWorkersAndTheConversationFreeForOtherMessages(t *testing.T) {
	script := deleteApprovalScript(`{"answer":"approve"}`)
	script.turnRouters = append(script.turnRouters, answerQuestionRoute("a question in another thread"), answerQuestionRoute("a question in another thread"))
	script.actions = []string{
		script.actions[0],
		connectorFinishMessage("내일 휴가로 등록된 일정 하나입니다."),
		script.actions[1],
	}
	fixture := newThreadAskFixture(t, script)
	repository := &testConnectorQueueRepository{}
	fixture.connectorRuntime.UseEventRepository(repository)
	otherThread := threadReplyEvent("message-2", "그게 어떤 일정이었지?")
	otherThread.ReplyTargetID = "reply-target-2"
	queueEvents(t, fixture, threadReplyEvent("message-1", threadAskRequest), otherThread)
	asking := runNextQueuedEvent(fixture.connectorRuntime)

	taskRunID := fixture.awaitQuestionOnTheThread(t)
	awaitSucceededEvents(t, repository, 1)

	queueEvents(t, fixture, threadReplyEvent("message-3", "ㅇㅇ"))
	fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	awaitFinished(t, asking)

	awaitSucceededEvents(t, repository, 3)
	taskRun, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if taskRun.Status != task.TaskStatusCompleted || fixture.invokedToolCount() != 1 {
		t.Fatalf("status %s with calls %v, expected the waiting run to finish once answered", taskRun.Status, fixture.invokedTools)
	}
}

func queueEvents(t *testing.T, fixture *threadAskFixture, events ...PlatformInboundEvent) {
	t.Helper()
	for _, event := range events {
		result, errorValue := fixture.connectorRuntime.HandleInboundEvent(context.Background(), fixture.adapter, event)
		if errorValue != nil || result.Reason != "queued" {
			t.Fatalf("the event was not queued: %+v %v", result, errorValue)
		}
	}
}

func runNextQueuedEvent(connectorRuntime *ConnectorRuntime) <-chan struct{} {
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}()
	return finished
}

func awaitFinished(t *testing.T, finished <-chan struct{}) {
	t.Helper()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker that carried the waiting run never finished it")
	}
}

func awaitSucceededEvents(t *testing.T, repository *testConnectorQueueRepository, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		repository.mutex.Lock()
		succeeded := len(repository.succeededEvents)
		repository.mutex.Unlock()
		if succeeded >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d queued events reached an outcome", count)
}

func TestAQuestionNobodyAnsweredInTimeRejectsTheHoldAndTheRunEndsTellingWhy(t *testing.T) {
	script := deleteApprovalScript()
	script.actions = []string{script.actions[0], connectorFinishMessageCiting("승인이 없어 일정을 삭제하지 않았습니다.", "obs-001")}
	fixture := newThreadAskFixture(t, script)
	fixture.connectorRuntime.askingThreads.expiry = 50 * time.Millisecond

	expired := fixture.await(t, fixture.send(context.Background(), threadReplyEvent("message-1", threadAskRequest)))

	taskRun, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(expired.TaskRunID)
	holds := holdrecord.Holds(fixture.connectorRuntime.taskRunService.ListTaskEvent(expired.TaskRunID))
	if taskRun.Status == task.TaskStatusWaitingApproval || fixture.invokedToolCount() != 0 || fixture.connectorRuntime.isAwaitedInThread(expired.TaskRunID) {
		t.Fatalf("after the expiry the run is %s with calls %v and awaited=%v, expected it ended without the call", taskRun.Status, fixture.invokedTools, fixture.connectorRuntime.isAwaitedInThread(expired.TaskRunID))
	}
	if len(holds) != 1 || holds[0].State != holdrecord.StateRejected || !connectorTaskEventsContain(fixture.connectorRuntime, expired.TaskRunID, approvalgate.TaskEventApprovalExpired, "") {
		t.Fatalf("holds %+v: expected the hold rejected with an expiry event", holds)
	}
}

func TestAHarnessPermissionRequestIsAskedInTheThreadAndAnsweredWithTheOfferedOption(t *testing.T) {
	fixture := newThreadAskFixture(t, threadAskScript{approvalReplies: []string{`{"answer":"allow-once"}`}})
	fixture.connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	taskRun := fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1", IsThread: true}, threadAskRequest)
	askingEvent := threadReplyEvent("message-1", threadAskRequest)
	approvalRequest := mcpserver.ApprovalRequest{RequesterPersonID: "person-1", TaskRunID: taskRun.TaskRunID, Platform: "test", ConversationID: "direct-1", ReplyTargetID: "reply-target-1"}
	question := approvalgate.HarnessPermissionQuestion{Text: "파일을 지울까요?", Options: []acp.PermissionOption{
		{OptionId: "allow-once", Kind: acp.PermissionOptionKindAllowOnce, Name: "allow"},
		{OptionId: "reject-once", Kind: acp.PermissionOptionKindRejectOnce, Name: "reject"},
	}}
	outcomes := make(chan acp.RequestPermissionOutcome, 1)
	go func() {
		outcome, _ := fixture.connectorRuntime.ThreadPermissionAsker().(approvalgate.HarnessPermissionAsker).AskHarnessPermission(withConnectorEvent(context.Background(), askingEvent), approvalRequest, question)
		outcomes <- outcome
	}()
	fixture.awaitQuestionOnTheThread(t)

	fixture.await(t, fixture.send(context.Background(), threadReplyEvent("message-2", "ㅇ")))

	select {
	case outcome := <-outcomes:
		if outcome.Selected == nil || outcome.Selected.OptionId != "allow-once" {
			t.Fatalf("the harness was answered %+v, expected the option the person's reply selected", outcome)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the harness was never answered")
	}
}

func TestOnlyTheRequesterWhoWasAskedIsOfferedTheirReply(t *testing.T) {
	threads := &askingThreads{}
	thread := &askingThread{taskRunID: "run-1", requesterPersonID: "person-1", platform: "test", conversationID: "direct-1"}
	threads.join(thread)

	if len(threads.awaiting("test", "direct-1", "person-2")) != 0 || len(threads.awaiting("test", "channel-1", "person-1")) != 0 || len(threads.awaiting("other", "direct-1", "person-1")) != 0 {
		t.Fatal("a question was offered to a reply from another person, conversation or platform")
	}
	if len(threads.awaiting("test", "direct-1", "person-1")) != 1 {
		t.Fatal("the requester's reply in the question's conversation was not offered the question")
	}
}

type progressCountingAdapter struct {
	*testAdapter
	mutex  sync.Mutex
	starts int
	stops  int
}

func (adapter *progressCountingAdapter) StartProgress(ctx context.Context, target ReplyTarget) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.starts++
	return nil
}

func (adapter *progressCountingAdapter) StopProgress(ctx context.Context, target ReplyTarget) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.stops++
	return nil
}

func (adapter *progressCountingAdapter) counts() (int, int) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	return adapter.starts, adapter.stops
}

func (adapter *progressCountingAdapter) isRunning() bool {
	starts, stops := adapter.counts()
	return starts > stops
}

func TestTheTypingIndicatorStopsWhileTheRunWaitsForAnAnswer(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript(`{"answer":"approve"}`))
	counting := &progressCountingAdapter{testAdapter: fixture.adapter}
	fixture.connectorRuntime.RegisterAdapter(counting)
	fixture.through = counting
	asking := fixture.send(context.Background(), threadReplyEvent("message-1", threadAskRequest))
	fixture.awaitQuestionOnTheThread(t)

	deadline := time.Now().Add(10 * time.Second)
	for counting.isRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if counting.isRunning() {
		t.Fatal("the typing indicator kept running while nobody had answered")
	}
	fixture.await(t, fixture.send(context.Background(), threadReplyEvent("message-2", "ㅇ")))
	fixture.await(t, asking)

	starts, _ := counting.counts()
	if counting.isRunning() || starts != 2 {
		t.Fatalf("progress started %d times and is running=%v, expected it to resume once the answer came", starts, counting.isRunning())
	}
}

func TestAnOperatorApprovalSettlesTheLiveWaiterAndTheAskingTurnContinuesInPlace(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	asking := fixture.send(context.Background(), threadReplyEvent("message-1", threadAskRequest))
	taskRunID := fixture.awaitQuestionOnTheThread(t)

	isAnswered, errorValue := fixture.connectorRuntime.AnswerAwaitedHold(taskRunID, agentcontract.ApprovalSignalApprove)
	result := fixture.await(t, asking)

	taskRun, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if errorValue != nil || !isAnswered || result.TaskRunID != taskRunID || fixture.taskRunCount() != 1 {
		t.Fatalf("answered=%v error=%v result=%+v runs=%d: expected the waiting run to continue alone", isAnswered, errorValue, result, fixture.taskRunCount())
	}
	if taskRun.Status != task.TaskStatusCompleted || fixture.invokedToolCount() != 1 {
		t.Fatalf("status %s with calls %v, expected one call on a completed run", taskRun.Status, fixture.invokedTools)
	}
}

func TestAnOperatorRejectionSettlesTheLiveWaiterWithoutRunningTheCall(t *testing.T) {
	script := deleteApprovalScript()
	script.actions = []string{script.actions[0], connectorFinishMessageCiting("승인되지 않아 삭제하지 않았습니다.", "obs-001")}
	fixture := newThreadAskFixture(t, script)
	asking := fixture.send(context.Background(), threadReplyEvent("message-1", threadAskRequest))
	taskRunID := fixture.awaitQuestionOnTheThread(t)

	isAnswered, _ := fixture.connectorRuntime.AnswerAwaitedHold(taskRunID, agentcontract.ApprovalSignalReject)
	fixture.await(t, asking)

	holds := holdrecord.Holds(fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRunID))
	if !isAnswered || len(holds) != 1 || holds[0].State != holdrecord.StateRejected || fixture.invokedToolCount() != 0 {
		t.Fatalf("answered=%v holds %+v calls %v: expected the hold rejected and nothing run", isAnswered, holds, fixture.invokedTools)
	}
}

func TestAnOperatorAnswerForARunNobodyIsWaitingOnIsNotSettledHere(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := restartedRunHoldingTheCall(t, fixture)

	isAnswered, errorValue := fixture.connectorRuntime.AnswerAwaitedHold(taskRun.TaskRunID, agentcontract.ApprovalSignalApprove)

	if isAnswered || errorValue != nil {
		t.Fatalf("answered=%v error=%v: with no live waiter the caller resumes the run itself", isAnswered, errorValue)
	}
}

func TestAHoldOlderThanTheExpiryAtBootRecordsTheExpiryAndTellsTheRequester(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := restartedRunHoldingTheCall(t, fixture)
	fixture.connectorRuntime.askingThreads.expiry = time.Millisecond
	time.Sleep(5 * time.Millisecond)

	fixture.connectorRuntime.reawaitHold(context.Background(), taskRun)

	ended, _ := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRun.TaskRunID)
	holds := holdrecord.Holds(fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(holds) != 1 || holds[0].State != holdrecord.StateRejected || !connectorTaskEventsContain(fixture.connectorRuntime, taskRun.TaskRunID, approvalgate.TaskEventApprovalExpired, "") {
		t.Fatalf("holds %+v: expected the hold rejected with approval.expired recorded as the live expiry does", holds)
	}
	if ended.Status != task.TaskStatusFailed || len(fixture.adapter.sentReplies) != 1 || fixture.adapter.sentReplies[0].message == "" {
		t.Fatalf("status %s with %d replies: expected the run failed and one model-worded notice sent to the thread", ended.Status, len(fixture.adapter.sentReplies))
	}
}
