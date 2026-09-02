package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/config"
	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/mcp"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/reply"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func TestConnectorRuntimeProcessesInvitedMessageAndDeduplicates(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "안녕하세요"}
	event := testInboundEvent("message-1")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	duplicateResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected duplicate event to process: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if result.ReplyDispatchID != "dispatch-1" {
		t.Fatalf("expected first dispatch id, got %q", result.ReplyDispatchID)
	}
	if !duplicateResult.Duplicate {
		t.Fatal("expected duplicate result")
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one reply, got %d", len(adapter.sentReplies))
	}
	if len(adapter.progressStarts) != 1 {
		t.Fatalf("expected one progress start, got %d", len(adapter.progressStarts))
	}
	if len(adapter.progressStops) != 1 {
		t.Fatalf("expected one progress stop, got %d", len(adapter.progressStops))
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "blueclaw.task.execution_duration", "durationMs") {
		t.Fatal("expected task execution duration event")
	}
}

func TestConnectorRuntimeSuppressesStaleRetryWhileOriginalTaskIsRunning(t *testing.T) {
	languageModel := &blockingTestLanguageModel{
		reply:   "done",
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	// Classification calls (e.g. skill-search-query) fall back to the main language
	// model when no intake model is configured, which would block on the same channel
	// as the task turn itself and fire "started" before the task is source-tagged. Give
	// classification its own non-blocking model so "started" reflects the actual turn.
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(testLanguageModel{reply: "classified"})
	event := testInboundEvent("message-stale-retry")
	firstResultChannel := make(chan ConnectorRuntimeResult, 1)
	firstErrorChannel := make(chan error, 1)

	go func() {
		result, errorValue := connectorRuntime.processInboundEventWithReplySender(context.Background(), adapter, event, adapter.SendReply)
		firstResultChannel <- result
		firstErrorChannel <- errorValue
	}()

	select {
	case <-languageModel.started:
	case <-time.After(time.Second):
		t.Fatal("expected original task to start")
	}

	retryContext, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	duplicateResult, errorValue := connectorRuntime.processInboundEventWithReplySender(retryContext, adapter, event, adapter.SendReply)
	if errorValue != nil {
		t.Fatalf("expected stale retry to suppress cleanly: %v", errorValue)
	}
	if !duplicateResult.Duplicate || duplicateResult.Reason != "duplicate_source_reference" {
		t.Fatalf("expected duplicate source suppression, got %+v", duplicateResult)
	}
	if duplicateResult.TaskRunID == "" {
		t.Fatal("expected duplicate result to point at original task")
	}
	if len(connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected one task run, got %+v", connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
	}
	if !connectorTaskEventsContain(connectorRuntime, duplicateResult.TaskRunID, "connector.duplicate_source_suppressed", event.MessageID) {
		t.Fatal("expected duplicate suppression event")
	}

	close(languageModel.release)
	select {
	case errorValue := <-firstErrorChannel:
		if errorValue != nil {
			t.Fatalf("expected original task to finish: %v", errorValue)
		}
	case <-time.After(time.Second):
		t.Fatal("expected original task to finish")
	}
	firstResult := <-firstResultChannel
	if firstResult.TaskRunID != duplicateResult.TaskRunID {
		t.Fatalf("expected duplicate to reuse %s, got %+v", firstResult.TaskRunID, duplicateResult)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected only original reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeDefersNewTaskLaunchWhenQuiesced(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	connectorRuntime.UseTaskIntakeGate(testTaskIntakeGate{isQuiesced: true})
	event := testInboundEvent("message-quiesced")
	adapter.httpParseResult = HTTPParseResult{HasEvent: true, Event: event}
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}

	result, _, errorValue := connectorRuntime.HandleHTTPEvent(context.Background(), adapter.Name(), request)
	if errorValue != nil {
		t.Fatalf("expected http event to queue: %v", errorValue)
	}
	if result.Reason != "queued" {
		t.Fatalf("expected queued result, got %+v", result)
	}
	if !connectorRuntime.processNextQueuedConnectorEvent(context.Background()) {
		t.Fatal("expected queued connector event to be claimed")
	}
	if len(repository.succeededEvents) != 0 {
		t.Fatalf("quiesced new task must not be marked succeeded, got %+v", repository.succeededEvents)
	}
	if len(repository.pendingReplies) != 0 || len(adapter.sentReplies) != 0 {
		t.Fatalf("quiesced new task must not reply, pending=%+v sent=%+v", repository.pendingReplies, adapter.sentReplies)
	}
}

func TestConnectorRuntimeAllowsWaitingTaskContinuationWhenQuiesced(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"continue_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"input reply","userFacingReply":""}`,
			},
		},
		ActionResponses: []string{connectorFinishMessage("continued while quiesced")},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	connectorRuntime.UseTaskIntakeGate(testTaskIntakeGate{isQuiesced: true})
	waitingTaskRun := createWaitingInputTaskRun(t, taskRunService, "single prompt", "single-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(waitingTaskRun, "single-dispatch", "single-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-continuation")
	event.ReplyTargetID = "single-dispatch"
	event.Prompt = "answer"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected continuation to process: %v", errorValue)
	}

	if result.TaskRunID != waitingTaskRun.TaskRunID {
		t.Fatalf("expected waiting task %s, got %+v", waitingTaskRun.TaskRunID, result)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "continued while quiesced" {
		t.Fatalf("expected continuation reply, got %+v", adapter.sentReplies)
	}
}

func TestExactStopCommandsAndActiveTaskFollowUpsBypassConversationLock(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	ctx := context.Background()

	stopEvent := testInboundEvent("message-stop")
	stopEvent.Prompt = "/stop"
	koreanStopEvent := testInboundEvent("message-stop-ko")
	koreanStopEvent.Prompt = "/중단"
	stopUnderscoreEvent := testInboundEvent("message-stop-underscore")
	stopUnderscoreEvent.Prompt = "/stop_all"
	debugEvent := testInboundEvent("message-debug")
	debugEvent.Prompt = "/debug"

	if !connectorRuntime.shouldProcessBeforeConversationLock(ctx, adapter, stopEvent) {
		t.Fatal("expected exact stop command to bypass conversation lock")
	}
	if connectorRuntime.shouldProcessBeforeConversationLock(ctx, adapter, koreanStopEvent) {
		t.Fatal("korean stop alias without an active task should not bypass conversation lock")
	}
	if connectorRuntime.shouldProcessBeforeConversationLock(ctx, adapter, stopUnderscoreEvent) {
		t.Fatal("underscore stop alias without an active task should not bypass conversation lock")
	}
	if connectorRuntime.shouldProcessBeforeConversationLock(ctx, adapter, debugEvent) {
		t.Fatal("debug message should keep conversation lock ordering")
	}
}

func TestActiveTaskFollowUpBypassesConversationLockWhenClassifiedAsRelated(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.IsActiveTaskFollowUp = true
	seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "보고서 작성")
	connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com", PersonID: "person-1"})

	event := testInboundEvent("message-correction")
	event.Prompt = "아니야 하지마"

	if !connectorRuntime.shouldProcessBeforeConversationLock(context.Background(), adapter, event) {
		t.Fatal("expected message classified as related to the active task to bypass conversation lock")
	}
}

func TestActiveTaskFollowUpDoesNotBypassConversationLockWhenClassifiedAsUnrelated(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.IsActiveTaskFollowUp = false
	seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "보고서 작성")
	connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com", PersonID: "person-1"})

	event := testInboundEvent("message-unrelated")
	event.Prompt = "오늘 날씨 어때"

	if connectorRuntime.shouldProcessBeforeConversationLock(context.Background(), adapter, event) {
		t.Fatal("expected unrelated message to keep conversation lock ordering")
	}
}

func TestActiveTaskFollowUpClassificationErrorDoesNotBypassConversationLock(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, testLanguageModel{errorValue: errors.New("model unavailable")})
	seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "보고서 작성")
	connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com", PersonID: "person-1"})

	event := testInboundEvent("message-correction-error")
	event.Prompt = "아니야 하지마"

	if connectorRuntime.shouldProcessBeforeConversationLock(context.Background(), adapter, event) {
		t.Fatal("expected classification failure to keep the safer conversation-lock ordering instead of bypassing")
	}
}

func TestConnectorRuntimeReplyTargetWaitResolvesOlderWaitingTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"continue_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"input reply","userFacingReply":""}`,
			},
		},
		ActionResponses: []string{connectorFinishMessage("older continued")},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	olderTaskRun := createWaitingInputTaskRun(t, taskRunService, "older prompt", "old-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(olderTaskRun, "old-dispatch", "old-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	time.Sleep(time.Millisecond)
	newerTaskRun := createWaitingInputTaskRun(t, taskRunService, "newer prompt", "new-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(newerTaskRun, "new-dispatch", "new-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-old-reply")
	event.ReplyTargetID = "old-dispatch"
	event.Prompt = "older answer"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected old wait reply to process: %v", errorValue)
	}

	if result.TaskRunID != olderTaskRun.TaskRunID {
		t.Fatalf("expected older task run %s, got %+v", olderTaskRun.TaskRunID, result)
	}
	olderTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(olderTaskRun.TaskRunID)
	newerTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(newerTaskRun.TaskRunID)
	if olderTaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected older task completed, got %+v", olderTaskRun)
	}
	if newerTaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("expected newer task to remain waiting, got %+v", newerTaskRun)
	}
	openWaits, errorValue := taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 1 || openWaits[0].TaskRunID != newerTaskRun.TaskRunID {
		t.Fatalf("expected only newer wait open, got %+v", openWaits)
	}
}

func TestConnectorRuntimeAmbiguousWaitDoesNotSelectNewest(t *testing.T) {
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, testLanguageModel{reply: "어느 작업에 답하셨나요?"})
	olderTaskRun := createWaitingInputTaskRun(t, taskRunService, "older prompt", "old-interaction")
	newerTaskRun := createWaitingInputTaskRun(t, taskRunService, "newer prompt", "new-interaction")
	for _, taskWaitToken := range []task.TaskWaitToken{
		waitRoutingTaskWaitToken(olderTaskRun, "old-dispatch", "old-interaction"),
		waitRoutingTaskWaitToken(newerTaskRun, "new-dispatch", "new-interaction"),
	} {
		if errorValue := taskWaitRepository.InsertTaskWaitToken(taskWaitToken); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	event := testInboundEvent("message-ambiguous")
	event.ReplyTargetID = "unmatched-reply-target"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected ambiguous wait to process: %v", errorValue)
	}

	if result.TaskRunID == olderTaskRun.TaskRunID || result.TaskRunID == newerTaskRun.TaskRunID {
		t.Fatalf("expected disambiguation task, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].taskRunID != result.TaskRunID {
		t.Fatalf("expected disambiguation reply, got %+v result=%+v", adapter.sentReplies, result)
	}
	olderTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(olderTaskRun.TaskRunID)
	newerTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(newerTaskRun.TaskRunID)
	if olderTaskRun.Status != task.TaskStatusWaitingUserInput || newerTaskRun.Status != task.TaskStatusWaitingUserInput {
		t.Fatalf("ambiguous reply must not continue waits, older=%s newer=%s", olderTaskRun.Status, newerTaskRun.Status)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "ask.requested", `"ask_input"`) {
		t.Fatalf("expected disambiguation ask_input, taskRunID=%s", result.TaskRunID)
	}
}

func TestConnectorRuntimeSingleOpenWaitFallbackContinuesTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"continue_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"input reply","userFacingReply":""}`,
			},
		},
		ActionResponses: []string{connectorFinishMessage("single continued")},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	waitingTaskRun := createWaitingInputTaskRun(t, taskRunService, "single prompt", "single-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(waitingTaskRun, "single-dispatch", "single-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-single")
	event.ReplyTargetID = "unmatched-reply-target"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected single wait fallback to process: %v", errorValue)
	}

	if result.TaskRunID != waitingTaskRun.TaskRunID {
		t.Fatalf("expected waiting task %s, got %+v", waitingTaskRun.TaskRunID, result)
	}
	waitingTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(waitingTaskRun.TaskRunID)
	if waitingTaskRun.Status != task.TaskStatusCompleted {
		t.Fatalf("expected waiting task completed, got %+v", waitingTaskRun)
	}
}

func TestConnectorRuntimePreservesNaturalLanguageOptionReply(t *testing.T) {
	const replyText = "발표자료로 만들어 주세요"
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"continue_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"natural-language reply selects the slides option","userFacingReply":"","choices":["B"]}`,
			},
		},
		ActionResponses: []string{connectorFinishMessage("발표자료로 진행했습니다.")},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	waitingTaskRun := createWaitingInputTaskRunWithOptions(t, taskRunService, "어떤 형식으로 만들까요?", "input-options")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(waitingTaskRun, "input-dispatch", "input-options")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-option")
	event.Prompt = replyText
	event.ReplyTargetID = "input-dispatch"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected option reply to process: %v", errorValue)
	}
	if result.TaskRunID != waitingTaskRun.TaskRunID {
		t.Fatalf("expected waiting task continuation, got %+v", result)
	}
	requests := languageModel.Requests()
	routerIndex := connectorSchemaIndexAfter(requests, "bluecollar_turn_router", -1)
	if routerIndex < 0 || !structuredMessagesContain(requests[routerIndex].Messages, replyText) {
		t.Fatalf("expected exact natural-language option reply in router request, got %+v", requests)
	}
	actionIndex := connectorSchemaIndexAfter(requests, "bluecollar_agent_turn_action", routerIndex)
	if actionIndex < 0 || !structuredMessagesContain(requests[actionIndex].Messages, replyText) {
		t.Fatalf("expected exact natural-language reply in resumed agent request, got %+v", requests)
	}
	if !connectorTaskEventsContain(connectorRuntime, waitingTaskRun.TaskRunID, "ask.resolved", `"choices":["B"]`) {
		t.Fatalf("expected selected option key in ask resolution, taskRunID=%s", waitingTaskRun.TaskRunID)
	}
	if structuredMessagesContain(requests[actionIndex].Messages, "User selected:") {
		t.Fatalf("expected no synthetic choice prompt, got %+v", requests[actionIndex].Messages)
	}
}

func TestConnectorRuntimePendingInputStartTaskSupersedesWaitingTask(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteStartTask,
		Classification:   agentcontract.IntakeClassificationQuickReply,
		TaskShape:        agentcontract.TaskShapeImmediateReply,
		TaskLevel:        agentcontract.TaskLevelXLow,
		ResponseLanguage: "ko",
		Reason:           "latest message is an independent question",
	}
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "휴게소 들러도 괜찮습니다."}
	taskWaitRepository := task.NewInMemoryTaskWaitTokenRepository()
	connectorRuntime.UseTaskWaitTokenRepository(taskWaitRepository)
	taskRunService := connectorRuntime.taskRunService
	waitingTaskRun := createWaitingInputTaskRun(t, taskRunService, "어느 채널에 보낼까요?", "single-interaction")
	if errorValue := taskWaitRepository.InsertTaskWaitToken(waitRoutingTaskWaitToken(waitingTaskRun, "single-dispatch", "single-interaction")); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-new-question")
	event.ReplyTargetID = "unmatched-reply-target"
	event.Prompt = "휴게소 가야해?"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected independent question to process: %v", errorValue)
	}

	if result.TaskRunID == "" || result.TaskRunID == waitingTaskRun.TaskRunID {
		t.Fatalf("expected new task result, got %+v", result)
	}
	waitingTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(waitingTaskRun.TaskRunID)
	if waitingTaskRun.Status != task.TaskStatusCancelled || waitingTaskRun.FailureReason != "superseded_by_new_message" {
		t.Fatalf("expected waiting task superseded, got %+v", waitingTaskRun)
	}
	openWaits, errorValue := taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 0 {
		t.Fatalf("expected superseded wait token to close, got %+v", openWaits)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "휴게소 들러도 괜찮습니다." {
		t.Fatalf("expected latest-message reply only, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, waitingTaskRun.TaskRunID, "ask.superseded_by_message", "message-new-question") {
		t.Fatal("expected ask superseded event")
	}
}

func TestConnectorRuntimeWritesResolvesAndExpiresTaskWaitRecord(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"siteRequestEvidence":"","responseLanguage":"ko","reason":"input needed","userFacingReply":"","initialToolNames":["ask_input"]}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","message":"추가 정보가 필요합니다.","toolName":"ask_input","toolInput":{"question":"추가 정보가 필요합니다."},"nextStepPlan":{"objective":"wait","expectedTools":[],"expectedNextResults":["user replies"],"doneCriteria":["reply received"],"risk":"none","workingSetReason":"ask_input waits for the user"}}`,
		},
	})
	connectorRuntime, adapter, taskRunService, taskWaitRepository := newWaitRoutingTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-send-wait")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected ask input send to process: %v", errorValue)
	}
	openWaits, errorValue := taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 1 {
		t.Fatalf("expected one open wait, got %+v", openWaits)
	}
	if openWaits[0].TaskRunID != result.TaskRunID || openWaits[0].ReplyTargetID != "dispatch-2" || openWaits[0].DispatchID != "dispatch-2" || openWaits[0].Kind != "input" {
		t.Fatalf("unexpected persisted wait: %+v result=%+v", openWaits[0], result)
	}
	if errorValue := taskWaitRepository.ResolveTaskWait(openWaits[0].WaitID, time.Now().UTC()); errorValue != nil {
		t.Fatal(errorValue)
	}
	openWaits, errorValue = taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 0 {
		t.Fatalf("expected resolved wait closed, got %+v", openWaits)
	}
	expiringTaskRun := createWaitingInputTaskRun(t, taskRunService, "expire prompt", "expire-interaction")
	expiringWait := waitRoutingTaskWaitToken(expiringTaskRun, "expire-dispatch", "expire-interaction")
	expiringWait.ExpiresAt = time.Now().Add(-time.Minute)
	if errorValue := taskWaitRepository.InsertTaskWaitToken(expiringWait); errorValue != nil {
		t.Fatal(errorValue)
	}
	expiredTaskRunIDs, errorValue := taskWaitRepository.ExpireOldTaskWaits(time.Now().UTC())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(expiredTaskRunIDs) != 1 || expiredTaskRunIDs[0] != expiringTaskRun.TaskRunID {
		t.Fatalf("expected expired task run id, got %+v", expiredTaskRunIDs)
	}
	openWaits, errorValue = taskWaitRepository.FindOpenByPersonAndConversation("person-1", "test", "direct-1")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(openWaits) != 0 {
		t.Fatalf("expected expired wait closed, got %+v", openWaits)
	}
}

func TestConnectorRuntimeStopCommandCancelsCurrentConversationTask(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	taskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "long task")
	event := testInboundEvent("message-stop")
	event.Prompt = "/stop"
	event.ReplyTargetID = event.MessageID

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected stop event to process: %v", errorValue)
	}
	if result.Reason != "task_control" {
		t.Fatalf("reason = %q, want task_control", result.Reason)
	}
	cancelledTaskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(taskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected cancelled task run, got found=%v run=%+v", isFound, cancelledTaskRun)
	}
	if len(adapter.sentReplies) != 1 || !strings.Contains(adapter.sentReplies[0].message, "1") {
		t.Fatalf("expected stop reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeStopCommandCancelsLatestThreadScopedTask(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	topLevelTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-1",
		IsThread:       false,
	}, "top level task")
	time.Sleep(time.Millisecond)
	threadTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-1",
		IsThread:       true,
	}, "thread task")
	time.Sleep(time.Millisecond)
	otherThreadTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-2",
		IsThread:       true,
	}, "other thread task")
	event := testChannelInboundEvent("message-stop")
	event.Prompt = "/stop"
	event.ReplyTargetID = "root-1"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected stop event to process: %v", errorValue)
	}
	if result.Reason != "task_control" {
		t.Fatalf("reason = %q, want task_control", result.Reason)
	}
	cancelledTaskRun, _ := connectorRuntime.taskRunService.FindTaskRun(threadTaskRun.TaskRunID)
	topLevelTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(topLevelTaskRun.TaskRunID)
	otherThreadTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(otherThreadTaskRun.TaskRunID)
	if cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected thread task cancelled, got %+v", cancelledTaskRun)
	}
	if topLevelTaskRun.Status == task.TaskStatusCancelled || otherThreadTaskRun.Status == task.TaskStatusCancelled {
		t.Fatalf("expected only matching thread task cancelled, top=%s other=%s", topLevelTaskRun.Status, otherThreadTaskRun.Status)
	}
}

func TestConnectorRuntimeStopCommandAtChannelRootCancelsLatestRootScopedTask(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	oldRootTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-old",
		IsThread:       false,
	}, "old root task")
	time.Sleep(time.Millisecond)
	latestRootTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "root-latest",
		IsThread:       false,
	}, "latest root task")
	time.Sleep(time.Millisecond)
	threadTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{
		ConversationID: "channel-1",
		ReplyTargetID:  "thread-root",
		IsThread:       true,
	}, "thread task")
	event := testChannelInboundEvent("message-stop")
	event.Prompt = "/stop"
	event.ReplyTargetID = event.MessageID

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected stop event to process: %v", errorValue)
	}
	if result.Reason != "task_control" {
		t.Fatalf("reason = %q, want task_control", result.Reason)
	}
	oldRootTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(oldRootTaskRun.TaskRunID)
	latestRootTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(latestRootTaskRun.TaskRunID)
	threadTaskRun, _ = connectorRuntime.taskRunService.FindTaskRun(threadTaskRun.TaskRunID)
	if latestRootTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected latest root task cancelled, got %+v", latestRootTaskRun)
	}
	if oldRootTaskRun.Status == task.TaskStatusCancelled || threadTaskRun.Status == task.TaskStatusCancelled {
		t.Fatalf("expected only latest root task cancelled, old=%s thread=%s", oldRootTaskRun.Status, threadTaskRun.Status)
	}
}

func TestConnectorRuntimeBusyStatusDoesNotCreateNewTask(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = agentcontract.TurnDecision{Route: agentcontract.TurnRouteAnswerQuestion, BusyRoute: agentcontract.BusyRouteStatus, Reason: "user asked for progress"}
	harness.Reply = "지금 처리 중입니다."
	activeTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "보고서 작성")
	if _, isFound := connectorRuntime.latestCurrentConversationActiveTask("person-1", "direct-1"); !isFound {
		t.Fatal("expected active task before busy status event")
	}
	event := testInboundEvent("message-busy-status")
	event.Prompt = "하고 있어?"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy status event to process: %v", errorValue)
	}
	if result.Reason != "busy_status" || result.TaskRunID != activeTaskRun.TaskRunID {
		t.Fatalf("expected busy status for active task, got %+v", result)
	}
	if len(connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected no new task run, got %+v", connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "지금 처리 중입니다." {
		t.Fatalf("expected status reply, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.status.requested", "message-busy-status") {
		t.Fatal("expected status request event")
	}
}

func TestConnectorRuntimeInterruptsInactiveRunningTaskAndStartsNewTask(t *testing.T) {
	now := time.Now()
	taskRunRepository := newTestTaskRunRepository()
	orphanedTaskRun := task.TaskRun{
		TaskRunID:            "task-orphaned",
		RequesterPersonID:    "person-1",
		OriginConversationID: "direct-1",
		CurrentAttemptID:     "attempt-orphaned",
		Status:               task.TaskStatusRunning,
		Prompt:               "멈춘 작업",
		CreatedAt:            now.Add(-time.Minute),
		UpdatedAt:            now.Add(-time.Minute),
	}
	taskRunRepository.taskRuns[orphanedTaskRun.TaskRunID] = orphanedTaskRun
	taskRunRepository.taskAttempts[orphanedTaskRun.CurrentAttemptID] = task.TaskAttempt{
		TaskAttemptID: orphanedTaskRun.CurrentAttemptID,
		TaskRunID:     orphanedTaskRun.TaskRunID,
		RunnerID:      "previous-runner",
		Status:        task.TaskAttemptStatusRunning,
		StartedAt:     now.Add(-time.Minute),
	}
	connectorRuntime, adapter, taskEventService, harness := newStubbedRepositoryBackedTestConnectorRuntime(t, taskRunRepository)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "새 작업으로 처리했습니다."}
	taskEventService.AppendTaskEvent(orphanedTaskRun.TaskRunID, "tool.site_build.requested", `{"observationID":"observation-1","toolName":"site_build"}`)
	event := testInboundEvent("message-after-stale-task")
	event.Prompt = "다시 해줘"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected event after inactive running task to process: %v", errorValue)
	}
	if result.TaskRunID == "" || result.TaskRunID == orphanedTaskRun.TaskRunID {
		t.Fatalf("expected new task after inactive task interruption, got %+v", result)
	}
	interruptedTaskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(orphanedTaskRun.TaskRunID)
	if !isFound || interruptedTaskRun.Status != task.TaskStatusInterrupted {
		t.Fatalf("expected inactive task interrupted, got found=%v task=%+v", isFound, interruptedTaskRun)
	}
	taskAttempt := taskRunRepository.taskAttempts[orphanedTaskRun.CurrentAttemptID]
	if taskAttempt.Status != task.TaskAttemptStatusInterrupted {
		t.Fatalf("attempt status = %s, want interrupted", taskAttempt.Status)
	}
	if !connectorTaskEventsContain(connectorRuntime, orphanedTaskRun.TaskRunID, "tool.site_build.cancelled", "cancelled_by_attempt_end") {
		t.Fatal("expected orphaned tool request to be cancelled")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "새 작업으로 처리했습니다." {
		t.Fatalf("expected new task reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeBusySteerAppendsInstructionWithoutNewTask(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = agentcontract.TurnDecision{Route: agentcontract.TurnRouteReviseTask, BusyRoute: agentcontract.BusyRouteSteer, BusyInstruction: "PDF 대신 HTML로 작성한다.", Reason: "user corrected active task"}
	harness.Reply = "방향 수정 내용을 현재 작업에 반영하겠습니다."
	activeTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "PDF 보고서 작성")
	if _, isFound := connectorRuntime.latestCurrentConversationActiveTask("person-1", "direct-1"); !isFound {
		t.Fatal("expected active task before busy steer event")
	}
	event := testInboundEvent("message-busy-steer")
	event.Prompt = "아니 PDF 말고 HTML로 해"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy steer event to process: %v", errorValue)
	}
	if result.Reason != "busy_steer" || result.TaskRunID != activeTaskRun.TaskRunID {
		t.Fatalf("expected busy steer for active task, got %+v", result)
	}
	if len(connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected no new task run, got %+v", connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.steer.requested", "PDF 대신 HTML") {
		t.Fatal("expected steer request event")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "방향 수정 내용을 현재 작업에 반영하겠습니다." {
		t.Fatalf("expected steer acknowledgement, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeBusyCancelStopsActiveTaskWithoutNewTask(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = agentcontract.TurnDecision{Route: agentcontract.TurnRouteConsume, BusyRoute: agentcontract.BusyRouteCancel, Reason: "user asked to cancel active task"}
	harness.Reply = "진행 중인 작업을 중단했습니다."
	activeTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "긴 작업")
	event := testInboundEvent("message-busy-cancel")
	event.Prompt = "중단해"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy cancel event to process: %v", errorValue)
	}
	cancelledTaskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(activeTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected active task cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
	if result.Reason != "busy_cancel" || result.TaskRunID != activeTaskRun.TaskRunID {
		t.Fatalf("expected busy cancel result, got %+v", result)
	}
	if len(connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected no new task run, got %+v", connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "진행 중인 작업을 중단했습니다." {
		t.Fatalf("expected cancel reply, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.cancel.requested", "message-busy-cancel") {
		t.Fatal("expected cancel request event")
	}
}

func TestConnectorRuntimeFollowUpReceivedBeforeTaskFinishedDoesNotCreateNewTask(t *testing.T) {
	taskRunRepository := newTestTaskRunRepository()
	finishedTaskRun := task.TaskRun{
		TaskRunID:            "task-finished",
		RequesterPersonID:    "person-1",
		OriginConversationID: "direct-1",
		Status:               task.TaskStatusCompleted,
		Prompt:               "보고서 작성",
		CreatedAt:            time.Now().Add(-time.Minute),
		UpdatedAt:            time.Now(),
	}
	taskRunRepository.taskRuns[finishedTaskRun.TaskRunID] = finishedTaskRun
	connectorRuntime, adapter, _, harness := newStubbedRepositoryBackedTestConnectorRuntime(t, taskRunRepository)
	harness.IsActiveTaskFollowUp = true
	harness.Reply = "그 작업은 이미 끝났습니다. 되돌릴까요, 아니면 새로 시작할까요?"

	event := testInboundEvent("message-after-finish")
	event.Prompt = "아니야 하지마"
	event.RawReceivedAt = finishedTaskRun.UpdatedAt.Add(-time.Millisecond)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected finished task follow-up to process: %v", errorValue)
	}
	if result.Reason != "busy_finished_followup" || result.TaskRunID != finishedTaskRun.TaskRunID {
		t.Fatalf("expected finished task follow-up result, got %+v", result)
	}
	if len(connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")) != 1 {
		t.Fatalf("expected no new task run to be created, got %+v", connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1"))
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "그 작업은 이미 끝났습니다. 되돌릴까요, 아니면 새로 시작할까요?" {
		t.Fatalf("expected finished task follow-up reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeUnrelatedMessageAfterFinishedTaskStartsNewTask(t *testing.T) {
	taskRunRepository := newTestTaskRunRepository()
	finishedTaskRun := task.TaskRun{
		TaskRunID:            "task-finished",
		RequesterPersonID:    "person-1",
		OriginConversationID: "direct-1",
		Status:               task.TaskStatusCompleted,
		Prompt:               "보고서 작성",
		CreatedAt:            time.Now().Add(-time.Minute),
		UpdatedAt:            time.Now(),
	}
	taskRunRepository.taskRuns[finishedTaskRun.TaskRunID] = finishedTaskRun
	connectorRuntime, adapter, _, harness := newStubbedRepositoryBackedTestConnectorRuntime(t, taskRunRepository)
	harness.IsActiveTaskFollowUp = false
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "새 주제로 진행했습니다."}

	event := testInboundEvent("message-new-topic")
	event.Prompt = "다른 걸 도와줘"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected unrelated message to process: %v", errorValue)
	}
	if result.TaskRunID == "" || result.TaskRunID == finishedTaskRun.TaskRunID {
		t.Fatalf("expected a new task run for an unrelated message, got %+v", result)
	}
}

func TestConnectorRuntimeBusyReplaceCancelsActiveTaskAndStartsNewTask(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = agentcontract.TurnDecision{Route: agentcontract.TurnRouteStartTask, BusyRoute: agentcontract.BusyRouteReplace, BusyInstruction: "새 지시로 교체한다.", Reason: "user replaced active task"}
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "새 작업으로 진행했습니다."}
	activeTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "기존 작업")
	event := testInboundEvent("message-busy-replace")
	event.Prompt = "아니 그거 취소하고 새 작업 해"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected busy replace event to process: %v", errorValue)
	}
	cancelledTaskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(activeTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled {
		t.Fatalf("expected active task cancelled, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
	if result.TaskRunID == "" || result.TaskRunID == activeTaskRun.TaskRunID {
		t.Fatalf("expected replacement task result, got %+v", result)
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.replaced", "message-busy-replace") {
		t.Fatal("expected replaced event")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "새 작업으로 진행했습니다." {
		t.Fatalf("expected replacement reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeBusyNewTaskSupersedesActiveTaskAndStartsNewTask(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = agentcontract.TurnDecision{Route: agentcontract.TurnRouteStartTask, BusyRoute: agentcontract.BusyRouteNewTask, Reason: "latest message is independent"}
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "휴게소 들러도 괜찮습니다."}
	activeTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1"}, "경산 영남대 근처 맛집 추천")
	event := testInboundEvent("message-independent-question")
	event.Prompt = "휴게소 가야해?"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected independent question to process: %v", errorValue)
	}

	cancelledTaskRun, isFound := connectorRuntime.taskRunService.FindTaskRun(activeTaskRun.TaskRunID)
	if !isFound || cancelledTaskRun.Status != task.TaskStatusCancelled || cancelledTaskRun.FailureReason != "superseded_by_new_message" {
		t.Fatalf("expected active task superseded, got found=%v task=%+v", isFound, cancelledTaskRun)
	}
	if result.TaskRunID == "" || result.TaskRunID == activeTaskRun.TaskRunID {
		t.Fatalf("expected new task result, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "휴게소 들러도 괜찮습니다." {
		t.Fatalf("expected latest task reply only, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, activeTaskRun.TaskRunID, "task.superseded_by_message", "message-independent-question") {
		t.Fatal("expected superseded event")
	}
}

func TestConnectorRuntimeDoesNotContinueFailedRecoverableArtifactGoal(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	taskRunService := connectorRuntime.taskRunService
	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "origin-reply-target"}, "기업 문서 가이드를 docx로 만들어줘")
	appendConnectorActiveGoal(t, taskRunService, taskRun, agentcontract.ActiveGoal{
		TaskRunID:           taskRun.TaskRunID,
		OriginalInstruction: taskRun.Prompt,
		Status:              agentcontract.ActiveGoalStatusBlocked,
		OutcomeContract: agentcontract.OutcomeContract{
			RequiredEvidenceTools:      []string{"file_attach"},
			RequiredAttachmentSuffixes: []string{".docx"},
			ExpectedResults: []agentcontract.ExpectedResult{{
				ID:       "attached-file",
				Type:     agentcontract.ExpectedResultTypeFile,
				Required: true,
			}},
			ArtifactRequirement: agentcontract.ArtifactRequirementRequired,
		},
	})
	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "file attach failed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-deliver")
	event.Prompt = "전달해줘야지 그럼"

	activeGoal, isFound := connectorRuntime.findActiveGoal("person-1", "", event, inboundTaskWaitResolution{})

	if isFound {
		t.Fatalf("expected failed artifact delivery task not to continue in the same task run, got %+v", activeGoal)
	}
}

func TestConnectorRuntimeFindsPriorTaskContextForFailedArtifactGoal(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	taskRunService := connectorRuntime.taskRunService
	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"}, "기업 문서 가이드를 docx로 만들어줘")
	appendConnectorActiveGoal(t, taskRunService, taskRun, agentcontract.ActiveGoal{
		TaskRunID:           taskRun.TaskRunID,
		OriginalInstruction: taskRun.Prompt,
		Status:              agentcontract.ActiveGoalStatusBlocked,
		OutcomeContract: agentcontract.OutcomeContract{
			RequiredEvidenceTools:      []string{"file_attach"},
			RequiredAttachmentSuffixes: []string{".docx"},
			ExpectedResults: []agentcontract.ExpectedResult{{
				ID:          "attached-file",
				Type:        agentcontract.ExpectedResultTypeFile,
				Description: "docx file attached to the current conversation",
				Required:    true,
			}},
			ArtifactRequirement: agentcontract.ArtifactRequirementRequired,
		},
	})
	intakeEvent, errorValue := json.Marshal(agentcontract.IntakeDecision{
		RequestedOutputFormats: []string{"docx"},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.intake", string(intakeEvent))
	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "file attach failed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-deliver")
	event.Prompt = "전달해줘야지 그럼"

	priorTaskContext, isFound := connectorRuntime.findPriorTaskContext("person-1", event)

	if !isFound {
		t.Fatal("expected failed artifact task to be available as prior context")
	}
	if priorTaskContext.TaskRunID != taskRun.TaskRunID {
		t.Fatalf("expected prior task run %q, got %+v", taskRun.TaskRunID, priorTaskContext)
	}
	if !agentcontract.OutcomeContractHasRequirements(priorTaskContext.OutcomeContract) {
		t.Fatalf("expected recoverable outcome contract, got %+v", priorTaskContext.OutcomeContract)
	}
	if !slices.Contains(priorTaskContext.OutcomeContract.RequiredEvidenceTools, "file_attach") {
		t.Fatalf("expected file.attach requirement, got %+v", priorTaskContext.OutcomeContract.RequiredEvidenceTools)
	}
	if !slices.Contains(priorTaskContext.OutcomeContract.RequiredAttachmentSuffixes, ".docx") {
		t.Fatalf("expected docx suffix requirement, got %+v", priorTaskContext.OutcomeContract.RequiredAttachmentSuffixes)
	}

	otherThreadEvent := testInboundEvent("message-other-thread")
	otherThreadEvent.Prompt = event.Prompt
	otherThreadEvent.ReplyTargetID = "other-reply-target"
	if _, isFound := connectorRuntime.findPriorTaskContext("person-1", otherThreadEvent); isFound {
		t.Fatal("expected different reply target not to receive prior task context")
	}
}

func TestConnectorRuntimeProvidesRecentPriorTaskContextWithoutTextInference(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	taskRunService := connectorRuntime.taskRunService
	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"}, "기업 문서 가이드를 워드 파일로 만들어줘")
	if _, errorValue := taskRunService.CompleteTaskRun(taskRun.TaskRunID, "요청하신 작업이 이미 성공적으로 완료되었습니다."); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-deliver-legacy")
	event.Prompt = "링크로 전달된 적 없어. 첨부파일로 줘야지 그리고."

	priorTaskContext, isFound := connectorRuntime.findPriorTaskContext("person-1", event)

	if !isFound {
		t.Fatal("expected recent completed task to be available as prior context")
	}
	if priorTaskContext.Prompt != "기업 문서 가이드를 워드 파일로 만들어줘" {
		t.Fatalf("expected prior prompt to be preserved for intake LLM judgment, got %+v", priorTaskContext)
	}
	if len(priorTaskContext.RequestedOutputFormats) != 0 {
		t.Fatalf("expected no runtime text-inferred output formats, got %+v", priorTaskContext.RequestedOutputFormats)
	}
}

func TestConnectorRuntimeDoesNotContinueFailedSiteOnlyGoal(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	taskRunService := connectorRuntime.taskRunService
	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "origin-reply-target"}, "웹사이트 만들어줘")
	appendConnectorActiveGoal(t, taskRunService, taskRun, agentcontract.ActiveGoal{
		TaskRunID:           taskRun.TaskRunID,
		OriginalInstruction: taskRun.Prompt,
		Status:              agentcontract.ActiveGoalStatusBlocked,
		OutcomeContract: agentcontract.OutcomeContract{
			RequiredEvidenceTools: []string{"site_serve"},
			ExpectedResults: []agentcontract.ExpectedResult{{
				ID:       "site-public-link",
				Type:     agentcontract.ExpectedResultTypeLink,
				Required: true,
			}},
			ArtifactRequirement: agentcontract.ArtifactRequirementNone,
		},
	})
	if _, errorValue := taskRunService.FailTaskRun(taskRun.TaskRunID, "publish failed"); errorValue != nil {
		t.Fatal(errorValue)
	}
	event := testInboundEvent("message-unrelated")
	event.Prompt = "전달해줘야지 그럼"

	_, isFound := connectorRuntime.findActiveGoal("person-1", "", event, inboundTaskWaitResolution{})

	if isFound {
		t.Fatal("expected failed site-only task not to continue through artifact recovery path")
	}
}

func TestOutboundReplyJSONPreservesInlineAttachmentPayload(t *testing.T) {
	reply := OutboundReply{
		Message:   "attached",
		TaskRunID: "task-1",
		ReplyKind: connectorReplyKindSuccess,
		Attachments: []toolcontract.FileAttachment{{
			DevicePath:    "/workspace/deck.pptx",
			Filename:      "deck.pptx",
			ContentType:   "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			SizeBytes:     4,
			ContentBase64: "cHB0eA==",
		}},
	}

	document, errorValue := json.Marshal(reply)
	if errorValue != nil {
		t.Fatalf("expected reply to marshal: %v", errorValue)
	}
	var decodedReply OutboundReply
	if errorValue := json.Unmarshal(document, &decodedReply); errorValue != nil {
		t.Fatalf("expected reply to unmarshal: %v", errorValue)
	}

	if len(decodedReply.Attachments) != 1 || decodedReply.Attachments[0].ContentBase64 != "cHB0eA==" {
		t.Fatalf("expected inline payload to survive outbox json, got %+v", decodedReply.Attachments)
	}
	if decodedReply.TaskRunID != "task-1" || decodedReply.ReplyKind != connectorReplyKindSuccess {
		t.Fatalf("expected delivery metadata to survive outbox json, got %+v", decodedReply)
	}
}

func TestOutboundReplyJSONPreservesAskInteraction(t *testing.T) {
	reply := OutboundReply{
		Message: "확인해 주세요.",
		Interaction: &AskInteraction{
			InteractionID:        "interaction-1",
			TaskRunID:            "task-1",
			Kind:                 "ask_confirm",
			Message:              "진행할까요?",
			TargetPlatformUserID: "user-1",
		},
	}

	document, errorValue := json.Marshal(reply)
	if errorValue != nil {
		t.Fatalf("expected reply to marshal: %v", errorValue)
	}
	var decodedReply OutboundReply
	if errorValue := json.Unmarshal(document, &decodedReply); errorValue != nil {
		t.Fatalf("expected reply to unmarshal: %v", errorValue)
	}

	if decodedReply.Interaction == nil || decodedReply.Interaction.Kind != "ask_confirm" || decodedReply.Interaction.Message != "진행할까요?" || decodedReply.Interaction.TargetPlatformUserID != "user-1" {
		t.Fatalf("expected ask interaction to survive outbox json, got %+v", decodedReply.Interaction)
	}
}

func TestOutboundReplyJSONPreservesFailureNotice(t *testing.T) {
	reply := OutboundReply{
		Message:   "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.",
		TaskRunID: "task-1",
		ReplyKind: connectorReplyKindUserNotice,
		FailureNotice: agentcontract.FailureNotice{
			Message:           "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.",
			Source:            "generated",
			Language:          "ko",
			DiagnosticEventID: "task-1:failed",
			IsSendable:        true,
		},
	}

	document, errorValue := json.Marshal(reply)
	if errorValue != nil {
		t.Fatalf("expected reply to marshal: %v", errorValue)
	}
	var decodedReply OutboundReply
	if errorValue := json.Unmarshal(document, &decodedReply); errorValue != nil {
		t.Fatalf("expected reply to unmarshal: %v", errorValue)
	}

	if decodedReply.FailureNotice.DiagnosticEventID != "task-1:failed" || !decodedReply.FailureNotice.IsSendable {
		t.Fatalf("expected failure notice to survive outbox json, got %+v", decodedReply.FailureNotice)
	}
}

func TestConnectorRuntimeStopsProgressAfterRequestContextCancellation(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	stopProgress := connectorRuntime.startProgress(ctx, adapter, ReplyTarget{
		ConversationID: "conversation-1",
		ReplyTargetID:  "reply-target-1",
	})

	cancel()
	stopProgress()

	if len(adapter.progressStops) != 1 {
		t.Fatalf("expected one progress stop, got %d", len(adapter.progressStops))
	}
	if adapter.progressStopErrors[0] != nil {
		t.Fatalf("expected stop progress context not to inherit cancellation, got %v", adapter.progressStopErrors[0])
	}
}

func TestConnectorRuntimeRejectsUninvitedUserWithoutTask(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	adapter.senderEmail = "outside@example.com"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected uninvited user to receive rejection: %v", errorValue)
	}

	if result.TaskRunID != "" {
		t.Fatalf("expected no task run, got %q", result.TaskRunID)
	}
	if result.ReplyDispatchID != "dispatch-1" {
		t.Fatalf("expected rejection dispatch id, got %q", result.ReplyDispatchID)
	}
	refusal := adapter.sentReplies[0].message
	if !strings.Contains(refusal, "outside@example.com") {
		t.Fatalf("a refusal that does not name the address it could not match leaves everyone guessing, got %q", refusal)
	}
	for _, registeredAddress := range []string{"person-1", "alice@example.com"} {
		if strings.Contains(refusal, registeredAddress) {
			t.Fatalf("whoever is asking may be from outside the company, so the roster stays unsaid, got %q", refusal)
		}
	}
}

func TestARefusalWithNoAddressSaysThatIsTheProblem(t *testing.T) {
	refusal := unmatchedAccountReplyFor(senderAuthorization{Platform: "mattermost", PlatformAccountEmail: "   "})

	if !strings.Contains(refusal, "no email address") {
		t.Fatalf("an account with no address matches nothing, and saying so is the whole diagnosis: %q", refusal)
	}
}

func TestConnectorRuntimeRequesterEmailFallsBackToVisibleSenderEmail(t *testing.T) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1"},
		},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	connectorRuntimeHarness := harnesstest.New(taskRunService)
	connectorRuntime := NewConnectorRuntime(identityService, connectorRuntimeHarness, taskRunService, taskEventService, nil)
	connectorRuntime.UseIntakeClassifier(connectorRuntimeHarness)
	connectorRuntime.UseReplyGenerator(connectorRuntimeHarness)
	event := testInboundEvent("message-1")
	event.Context.Sender.Email = "Sender@Example.com"

	email := connectorRuntime.requesterEmailForEvent("person-1", event)

	if email != "sender@example.com" {
		t.Fatalf("email = %q", email)
	}
}

func TestConnectorRuntimeRequesterEmailPrefersPolicyPrimaryEmail(t *testing.T) {
	identityService := identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"primary@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1"},
		},
	})
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	connectorRuntimeHarness := harnesstest.New(taskRunService)
	connectorRuntime := NewConnectorRuntime(identityService, connectorRuntimeHarness, taskRunService, taskEventService, nil)
	connectorRuntime.UseIntakeClassifier(connectorRuntimeHarness)
	connectorRuntime.UseReplyGenerator(connectorRuntimeHarness)
	event := testInboundEvent("message-1")
	event.Context.Sender.Email = "sender@example.com"

	email := connectorRuntime.requesterEmailForEvent("person-1", event)

	if email != "primary@example.com" {
		t.Fatalf("email = %q", email)
	}
}

func TestConnectorRuntimeSkipsAddressingClassifierForDirectMessage(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "ok"}
	event := testInboundEvent("message-1")
	event.Context.ConversationType = "D"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected direct message to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected direct message task and reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if harness.ClassifyAddressingCallCount() != 0 {
		t.Fatalf("expected direct message to skip addressing classifier, got %d classifications", harness.ClassifyAddressingCallCount())
	}
}

func TestConnectorRuntimeReactsToConsumedAddressedMessageWithoutReply(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnResult = agentcontract.AgentTurnResult{TurnRoute: agentcontract.TurnRouteConsume, ReactionEmojiName: "tada"}
	event := testInboundEvent("message-consume")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected consume event to process: %v", errorValue)
	}

	if result.Reason != "consume_reacted" || result.TaskRunID == "" {
		t.Fatalf("expected consume reaction result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no reply for consume, got %+v", adapter.sentReplies)
	}
	if len(adapter.reactions) != 1 {
		t.Fatalf("expected one reaction, got %+v", adapter.reactions)
	}
	reaction := adapter.reactions[0]
	if reaction.MessageID != event.MessageID || reaction.EmojiName != "tada" || reaction.Reason != "consume" {
		t.Fatalf("unexpected consume reaction: %+v", reaction)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "connector.reaction.sent", "tada") {
		t.Fatal("expected reaction event")
	}
}

func TestConnectorRuntimeDirectConsumeFallsBackToReplyWhenReactionFails(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnResult = agentcontract.AgentTurnResult{TurnRoute: agentcontract.TurnRouteConsume, ReactionEmojiName: "tada", FinishMessage: "알겠습니다."}
	adapter.reactionError = errors.New("reaction failed")
	event := testInboundEvent("message-direct-consume")
	event.Context.ConversationType = "D"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected direct consume fallback to process: %v", errorValue)
	}

	if result.Reason != "consume_fallback_sent" || result.ReplyDispatchID == "" {
		t.Fatalf("expected direct consume fallback reply, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "알겠습니다." {
		t.Fatalf("expected model-authored fallback reply, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "connector.reaction.failed", "reaction failed") {
		t.Fatal("expected reaction failure event before fallback")
	}
}

func TestConnectorRuntimeDirectConsumeFallsBackWithoutReactionAdapter(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnResult = agentcontract.AgentTurnResult{TurnRoute: agentcontract.TurnRouteConsume, ReactionEmojiName: "white_check_mark", FinishMessage: "확인했습니다."}
	event := testInboundEvent("message-direct-consume")
	event.Context.ConversationType = "D"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), testAdapterWithoutReaction{adapter: adapter}, event)
	if errorValue != nil {
		t.Fatalf("expected direct consume fallback to process: %v", errorValue)
	}

	if result.Reason != "consume_fallback_sent" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected fallback reply without reaction adapter, result=%+v replies=%+v", result, adapter.sentReplies)
	}
}

func TestConnectorRuntimeConsumeWithoutReactionAdapterDoesNotReply(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnResult = agentcontract.AgentTurnResult{TurnRoute: agentcontract.TurnRouteConsume}
	noReactionAdapter := testAdapterWithoutReaction{adapter: adapter}

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), noReactionAdapter, testInboundEvent("message-consume"))
	if errorValue != nil {
		t.Fatalf("expected consume event to process: %v", errorValue)
	}

	if result.Reason != "consume_no_reaction_adapter" {
		t.Fatalf("expected no-adapter consume result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 || len(adapter.reactions) != 0 {
		t.Fatalf("expected no reply or reaction, replies=%+v reactions=%+v", adapter.sentReplies, adapter.reactions)
	}
}

func TestConnectorRuntimeReactionFailureDoesNotSendFallbackReply(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnResult = agentcontract.AgentTurnResult{TurnRoute: agentcontract.TurnRouteConsume}
	adapter.reactionError = errors.New("reaction failed")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-consume"))
	if errorValue != nil {
		t.Fatalf("expected consume event to process: %v", errorValue)
	}

	if result.Reason != "consume_reaction_failed" {
		t.Fatalf("expected reaction failure result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no fallback reply, got %+v", adapter.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "connector.reaction.failed", "reaction failed") {
		t.Fatal("expected reaction failure event")
	}
}

func TestLatestApprovalQuestionUsesOnlyUserFacingMessage(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		Name: "confirmation.requested",
		Body: `{"reason":"Direct messages are external sends and require approval before immediate delivery.","reasonCode":"external_send"}`,
	}, {
		Name: "confirmation.requested",
		Body: `{"userFacingMessage":"샘플 님에게 다음 DM을 보내도 될까요?\n\n테스트","reasonDetail":"internal only"}`,
	}}

	question := latestApprovalQuestion(taskEvents)

	if question != "샘플 님에게 다음 DM을 보내도 될까요?\n\n테스트" {
		t.Fatalf("expected user-facing approval question, got %q", question)
	}
}

func TestLatestApprovalQuestionDoesNotFallBackToReason(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		Name: "confirmation.requested",
		Body: `{"reason":"Direct messages are external sends and require approval before immediate delivery.","reasonCode":"external_send"}`,
	}}

	question := latestApprovalQuestion(taskEvents)

	if question != "" {
		t.Fatalf("expected no question from internal reason, got %q", question)
	}
}

func TestLatestAskInteractionSkipsResolvedInteraction(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		TaskEventID: "ask-1",
		Name:        "ask.requested",
		Body:        `{"kind":"choice_single","question":"배포할 사이트를 선택해 주세요.","options":[{"key":"A","label":"첫 번째"},{"key":"B","label":"두 번째"}]}`,
	}, {
		TaskEventID: "resolved-1",
		Name:        "ask.resolved",
		Body:        `{"interactionID":"ask-1","kind":"ask_choice_single","choices":["B"]}`,
	}}

	interaction, isFound := latestAskInteraction("task-1", taskEvents)

	if isFound {
		t.Fatalf("expected resolved ask interaction to be hidden, got %+v", interaction)
	}
}

func TestLatestAskInteractionPreservesLegacyMultipleSelectionMode(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		TaskEventID: "ask-1",
		Name:        "ask.requested",
		Body:        `{"kind":"choice_multiple","question":"필요한 형식을 선택해 주세요.","choices":["PDF","PPTX"]}`,
	}}

	interaction, isFound := latestAskInteraction("task-1", taskEvents)

	if !isFound || interaction.Kind != "ask_input" || interaction.SelectionMode != "multiple" || len(interaction.Options) != 2 {
		t.Fatalf("expected canonical multiple input interaction, got found=%v interaction=%+v", isFound, interaction)
	}
}

func TestLatestAskInteractionReturnsNewAskAfterEarlierResolution(t *testing.T) {
	taskEvents := []task.TaskEvent{{
		TaskEventID: "ask-1",
		Name:        "ask.requested",
		Body:        `{"kind":"choice_single","question":"배포할 사이트를 선택해 주세요.","options":[{"key":"A","label":"첫 번째"},{"key":"B","label":"두 번째"}]}`,
	}, {
		TaskEventID: "resolved-1",
		Name:        "ask.resolved",
		Body:        `{"interactionID":"ask-1","kind":"ask_choice_single","choices":["B"]}`,
	}, {
		TaskEventID: "ask-2",
		Name:        "ask.requested",
		Body:        `{"kind":"confirm","message":"복구를 진행할까요?"}`,
	}}

	interaction, isFound := latestAskInteraction("task-1", taskEvents)

	if !isFound || interaction.InteractionID != "ask-2" || interaction.Kind != "ask_confirm" {
		t.Fatalf("expected latest unresolved ask interaction, got found=%v interaction=%+v", isFound, interaction)
	}
}

func TestConnectorRuntimeProcessesBotMentionThroughAddressingClassifier(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingTarget: string(agentcontract.AddressingTargetBot), reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testChannelInboundEvent("message-1")
	event.Context.Addressing.BotMentioned = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected bot mention to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected bot mention task and reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if !connectorContainsSchemaName(languageModel.requests, "bluecollar_addressing_classification") {
		t.Fatalf("expected bot mention to run the addressing classifier, got schemas %+v", connectorRequestSchemaNames(languageModel.requests))
	}
}

func TestConnectorRuntimeIgnoresOtherPersonMentionWithoutDuty(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingTarget: string(agentcontract.AddressingTargetHuman), reply: "unused"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testChannelInboundEvent("message-1")
	event.Context.Addressing.OtherPersonMentioned = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected other mention to be ignored: %v", errorValue)
	}

	if !result.Ignored || !strings.HasPrefix(result.Reason, "addressing_") {
		t.Fatalf("expected other-person mention without duty to be classified then ignored, got %+v", result)
	}
	if result.TaskRunID != "" || len(adapter.sentReplies) != 0 || len(adapter.progressStarts) != 0 {
		t.Fatalf("expected no task/reply/progress, got result=%+v replies=%d progress=%d", result, len(adapter.sentReplies), len(adapter.progressStarts))
	}
	if len(adapter.reactions) != 0 {
		t.Fatalf("expected addressing ignored message not to receive reaction, got %+v", adapter.reactions)
	}
	if !connectorContainsSchemaName(languageModel.requests, "bluecollar_addressing_classification") {
		t.Fatalf("expected other-person mention to be classified for standing-duty capture, got schemas %+v", connectorRequestSchemaNames(languageModel.requests))
	}
}

func TestConnectorRuntimeProcessesAssistantRequestedAmbiguousChannelMessage(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingTarget: string(agentcontract.AddressingTargetBot), reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected assistant-requested message to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected assistant-requested task and reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if !connectorContainsSchemaName(languageModel.requests, "bluecollar_addressing_classification") {
		t.Fatalf("expected addressing classifier request, got schemas %+v", connectorRequestSchemaNames(languageModel.requests))
	}
}

func TestConnectorRuntimeUsesIntakeLanguageModelForAddressingClassifier(t *testing.T) {
	replyLanguageModel := &addressingTestLanguageModel{addressingTarget: string(agentcontract.AddressingTargetHuman), reply: "ok"}
	intakeLanguageModel := &addressingTestLanguageModel{addressingTarget: string(agentcontract.AddressingTargetBot), reply: "unused"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, replyLanguageModel)
	connectorRuntime.UseIntakeClassifier(intake.NewClassifier(intakeLanguageModel))

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected intake classifier to process: %v", errorValue)
	}

	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected intake classifier result to launch task, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if !connectorContainsSchemaName(intakeLanguageModel.requests, "bluecollar_addressing_classification") {
		t.Fatalf("expected intake language model to classify addressing, got schemas %+v", connectorRequestSchemaNames(intakeLanguageModel.requests))
	}
	if connectorContainsSchemaName(replyLanguageModel.requests, "bluecollar_addressing_classification") {
		t.Fatalf("expected reply language model not to classify addressing, got schemas %+v", connectorRequestSchemaNames(replyLanguageModel.requests))
	}
}

func TestConnectorRuntimeIgnoresNonAssistantAddressingClasses(t *testing.T) {
	tests := []struct {
		name             string
		addressingTarget agentcontract.AddressingTarget
		reason           string
	}{
		{name: "human", addressingTarget: agentcontract.AddressingTargetHuman, reason: "addressing_human dutyMatch=false"},
		{name: "anyone", addressingTarget: agentcontract.AddressingTargetAnyone, reason: "addressing_anyone dutyMatch=false"},
		{name: "none", addressingTarget: agentcontract.AddressingTargetNone, reason: "addressing_none dutyMatch=false"},
		{name: "unclear", addressingTarget: agentcontract.AddressingTargetUnclear, reason: "addressing_unclear dutyMatch=false"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
			harness.AddressingDecision = agentcontract.AddressingDecision{Target: test.addressingTarget}

			result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
			if errorValue != nil {
				t.Fatalf("expected message to be ignored: %v", errorValue)
			}

			if !result.Ignored || result.Reason != test.reason {
				t.Fatalf("expected %s ignore, got %+v", test.reason, result)
			}
			if result.TaskRunID != "" || len(adapter.sentReplies) != 0 || len(adapter.progressStarts) != 0 {
				t.Fatalf("expected no task/reply/progress, got result=%+v replies=%d progress=%d", result, len(adapter.sentReplies), len(adapter.progressStarts))
			}
		})
	}
}

func TestConnectorRuntimeIgnoresUninvitedAmbiguousChannelMessageWithoutReply(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.AddressingDecision = agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true}
	adapter.senderEmail = "outside@example.com"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected uninvited ambiguous message to be ignored: %v", errorValue)
	}

	if !result.Ignored || result.Reason != "not_addressed_to_bot" {
		t.Fatalf("expected not_addressed_to_bot ignore, got %+v", result)
	}
	if result.TaskRunID != "" || len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no task or not-invited reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
}

func TestConnectorRuntimeIgnoresWhenAddressingClassifierFails(t *testing.T) {
	languageModel := &addressingTestLanguageModel{addressingError: errors.New("classifier unavailable"), reply: "unused"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testChannelInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected classifier failure to close the gate: %v", errorValue)
	}

	if !result.Ignored || result.Reason != "addressing_classifier_failed dutyMatch=false" {
		t.Fatalf("expected addressing_classifier_failed ignore, got %+v", result)
	}
	if result.TaskRunID != "" || len(adapter.sentReplies) != 0 || len(adapter.progressStarts) != 0 {
		t.Fatalf("expected no task/reply/progress, got result=%+v replies=%d progress=%d", result, len(adapter.sentReplies), len(adapter.progressStarts))
	}
}

func TestConnectorRuntimeDoesNotFilterUserNoticeAttachmentClaimText(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.Prompt = "파일 만들어줘"
	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{UserNotice: "파일을 생성해 첨부했습니다."},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected user notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || sentReplies[0].Message != "파일을 생성해 첨부했습니다." {
		t.Fatalf("expected unchanged user notice reply, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeSendsAskUserNoticeForTargetUser(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	connectorRuntime.taskRunService.AppendTaskEvent("task-1", "ask.requested", `{"kind":"input","question":"제목은 어떻게 할까요?"}`)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.SenderID = "requester-1"

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{UserNotice: "제목은 어떻게 할까요?"},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected ask user notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || sentReplies[0].Interaction == nil {
		t.Fatalf("expected ask interaction reply, got %+v", sentReplies)
	}
	if sentReplies[0].Interaction.TargetPlatformUserID != "requester-1" {
		t.Fatalf("expected interaction target to remain requester-scoped, got %+v", sentReplies[0].Interaction)
	}
}

func TestConnectorRuntimePassesThroughUserNoticeWithoutContentFiltering(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.Prompt = "html 파일 만들어줘"
	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{UserNotice: "작업 결과는 sandbox:/mnt/data/Hermes_Agent_Slide_Part1.html에 있습니다."},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected user notice passthrough, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || sentReplies[0].Message != "작업 결과는 sandbox:/mnt/data/Hermes_Agent_Slide_Part1.html에 있습니다." {
		t.Fatalf("expected exact model wording, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeSendsSafeUserNoticeForBlockedTask(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{UserNotice: "PPTX를 만들지 못했습니다. 다시 시도해 주세요."},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected user notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || sentReplies[0].ReplyKind != connectorReplyKindUserNotice || sentReplies[0].TaskRunID != "task-1" {
		t.Fatalf("expected user notice reply metadata, got %+v", sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, "task-1", "connector.reply.sent", "user_notice") {
		t.Fatal("expected sent event for user notice")
	}
}

func TestConnectorRuntimeSendsFailureNoticeForBlockedTask(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{
			TaskRun:    task.TaskRun{Status: task.TaskStatusBlocked},
			UserNotice: "replyStatus: raw internal diagnostic",
			FailureNotice: agentcontract.FailureNotice{
				Message:           "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.",
				Source:            "generated",
				Language:          "ko",
				DiagnosticEventID: "task-1:limit",
				IsSendable:        true,
			},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected failure notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || sentReplies[0].Message != "작업을 완료하지 못했습니다. 접근 권한을 확인한 뒤 다시 시도해 주세요.\n\n`task-1`" {
		t.Fatalf("expected failure notice reply with run footer, got %+v", sentReplies)
	}
	if sentReplies[0].FailureNotice.DiagnosticEventID != "task-1:limit" {
		t.Fatalf("expected diagnostic reference to be preserved, got %+v", sentReplies[0].FailureNotice)
	}
}

func TestConnectorRuntimeFailureFooterLinksAdminTaskWhenConfigured(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	connectorRuntime.UseAdminTaskLinkBaseURL("https://demo.example.test/")
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	_, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"a1b2c3d4e5f6",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{
			TaskRun: task.TaskRun{Status: task.TaskStatusFailed},
			FailureNotice: agentcontract.FailureNotice{
				Message:    "작업을 완료하지 못했습니다.",
				Source:     "generated",
				Language:   "ko",
				IsSendable: true,
			},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || len(sentReplies) != 1 {
		t.Fatalf("expected failure notice to send, got %+v", sentReplies)
	}
	if !strings.HasSuffix(sentReplies[0].Message, "[`a1b2c3`](https://demo.example.test/tasks/a1b2c3d4e5f6)") {
		t.Fatalf("expected admin task link footer, got %q", sentReplies[0].Message)
	}
}

func TestConnectorRuntimeWaitingNoticeHasNoRunFooter(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	_, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{
			TaskRun:    task.TaskRun{Status: task.TaskStatusWaitingUserInput},
			UserNotice: "범위를 알려주시면 진행할게요.",
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || len(sentReplies) != 1 {
		t.Fatalf("expected waiting notice to send, got %+v", sentReplies)
	}
	if sentReplies[0].Message != "범위를 알려주시면 진행할게요." {
		t.Fatalf("expected no run footer on waiting notice, got %q", sentReplies[0].Message)
	}
}

func TestConnectorRuntimeSendsSafeUserNoticeWhenFailureNoticeMissing(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{
			TaskRun:    task.TaskRun{Status: task.TaskStatusBlocked},
			UserNotice: "메시지 삭제 작업을 완료하지 못했습니다.",
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || dispatchID != "dispatch-1" {
		t.Fatalf("expected safe fallback notice to send, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 1 || !strings.HasPrefix(sentReplies[0].Message, "메시지 삭제 작업을 완료하지 못했습니다.") || !strings.Contains(sentReplies[0].Message, "\n\n`") {
		t.Fatalf("expected fallback user notice reply with run footer, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeSendsGenericFailureNoticeWhenFailureReplyMissing(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")

	dispatchID, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{
			TaskRun: task.TaskRun{Status: task.TaskStatusFailed},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if isSent || dispatchID != "" {
		t.Fatalf("expected missing generated failure notice to be suppressed, got dispatchID=%q sent=%v", dispatchID, isSent)
	}
	if len(sentReplies) != 0 {
		t.Fatalf("expected no generic failure reply, got %+v", sentReplies)
	}
}

func TestConnectorRuntimeAddsSenderToRecoveryActions(t *testing.T) {
	connectorRuntime, _, _ := newStubbedTestConnectorRuntime(t)
	sentReplies := []OutboundReply{}
	event := testInboundEvent("message-1")
	event.SenderID = "sender-user-1"

	_, isSent := connectorRuntime.sendUserNoticeReply(
		context.Background(),
		"test",
		event,
		"task-1",
		ReplyTarget{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"},
		agentcontract.AgentTurnResult{
			UserNotice: "Companion 연결이 필요합니다.",
			RecoveryActions: []toolcontract.RecoveryAction{{
				Kind:           "companion_connect",
				Delivery:       "dm_preferred",
				DownloadURL:    "https://example.com/companion.dmg",
				ConnectCommand: "/connect",
			}},
		},
		func(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
			sentReplies = append(sentReplies, reply)
			return "dispatch-1", nil
		},
	)

	if !isSent || len(sentReplies) != 1 || len(sentReplies[0].RecoveryActions) != 1 {
		t.Fatalf("expected recovery reply, got sent=%v replies=%+v", isSent, sentReplies)
	}
	if sentReplies[0].RecoveryActions[0].PlatformUserID != "sender-user-1" {
		t.Fatalf("expected sender recovery target, got %+v", sentReplies[0].RecoveryActions[0])
	}
}

func TestConnectorRuntimeSendsFailureNoticeWhenTurnReturnsError(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntimeRoutingWith(t, testLanguageModel{reply: "요청을 분류하지 못해 작업을 시작하지 못했습니다. 다시 요청해 주세요."}, testLanguageModel{errorValue: errors.New("provider unavailable")})

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected turn error to be reported to the user: %v", errorValue)
	}

	if result.Reason != "task_not_completed" || result.TaskRunID == "" {
		t.Fatalf("expected task failure result, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || !strings.Contains(adapter.sentReplies[0].message, "분류하지 못해") {
		t.Fatalf("expected task failure notice, got %+v", adapter.sentReplies)
	}
	if adapter.sentReplies[0].failureNotice.Source != "generated" {
		t.Fatalf("expected LLM-authored failure notice, got %+v", adapter.sentReplies[0].failureNotice)
	}
	if !connectorTaskEventsContain(connectorRuntime, result.TaskRunID, "llm.call", `"isError":true`) {
		t.Fatal("expected persisted router call error")
	}
}

func TestConnectorRuntimeSendsNoticeWhenLaunchReturnsNoTask(t *testing.T) {
	connectorRuntime, adapter := newTestConnectorRuntime(t, nil)

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected launch error to be reported to the user: %v", errorValue)
	}

	if result.Reason != "task_not_completed" || result.TaskRunID == "" {
		t.Fatalf("expected launch failure result, got %+v", result)
	}
	if len(adapter.sentReplies) != 1 || !strings.Contains(adapter.sentReplies[0].message, "turn router language model unavailable") {
		t.Fatalf("expected launch failure notice, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeUsesOpaqueReplyTarget(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "reply"}
	event := testInboundEvent("message-1")
	event.ReplyTargetID = "opaque-reply-target"

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if adapter.sentReplies[0].target.ReplyTargetID != "opaque-reply-target" {
		t.Fatalf("expected opaque reply target, got %q", adapter.sentReplies[0].target.ReplyTargetID)
	}
}

func TestConnectorRuntimeStartsDirectProgressBeforeInitialHistoryFetch(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "reply"}
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true
	event.Context.HistoryCursor = "history-cursor-1"

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(adapter.operationNames) < 2 || adapter.operationNames[0] != "progress.start" || adapter.operationNames[1] != "history.fetch" {
		t.Fatalf("expected progress before history fetch, got %+v", adapter.operationNames)
	}
}

func TestConnectorRuntimeInjectsRequesterPinnedMemoryIntoLanguageModel(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "기억했습니다"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	memoryRepository := memory.NewInMemoryRepository()
	if errorValue := memoryRepository.SaveProfile(context.Background(), memory.Profile{PersonID: "person-1", IdentityLines: []string{"사용자는 fact 저장소 메모리 설계를 선택했다."}}); errorValue != nil {
		t.Fatalf("expected memory profile setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(&memory.Store{Facts: memoryRepository, Profiles: memoryRepository, Jobs: memoryRepository}, nil)
	connectorRuntime.UseTaskLauncher(connectorRuntime.routedTaskLauncherForTest(toolCatalogBuilder))

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1"))
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(languageModel.request.Messages) < 2 {
		t.Fatalf("expected memory context message, got %+v", languageModel.request.Messages)
	}
	if !structuredMessagesContain(languageModel.request.Messages, "fact 저장소 메모리 설계") {
		t.Fatalf("expected requester memory in model context, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeInjectsVisibleContextBeforeMemory(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "맥락 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context = VisibleContext{
		Messages: []VisibleContextMessage{
			{Speaker: "admin", Text: "이전 메시지"},
		},
		HasMoreBefore: true,
		HistoryCursor: "cursor-1",
	}
	memoryRepository := memory.NewInMemoryRepository()
	if errorValue := memoryRepository.SaveProfile(context.Background(), memory.Profile{PersonID: "person-1", IdentityLines: []string{"사용자는 간결한 설계를 선호한다."}}); errorValue != nil {
		t.Fatalf("expected memory profile setup to succeed: %v", errorValue)
	}
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(&memory.Store{Facts: memoryRepository, Profiles: memoryRepository, Jobs: memoryRepository}, nil)
	connectorRuntime.UseTaskLauncher(connectorRuntime.routedTaskLauncherForTest(toolCatalogBuilder))

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	visibleContextIndex := messageIndex(languageModel.request.Messages, "admin: 이전 메시지")
	memoryIndex := messageIndex(languageModel.request.Messages, "간결한 설계")
	promptIndex := userMessageIndex(languageModel.request.Messages, event.Prompt)
	if visibleContextIndex < 0 || memoryIndex < 0 || promptIndex < 0 {
		t.Fatalf("expected visible context, memory, and prompt messages, got %+v", languageModel.request.Messages)
	}
	contextBody := joinConnectorMessageContent(languageModel.request.Messages)
	visibleContextTextIndex := strings.Index(contextBody, "admin: 이전 메시지")
	memoryTextIndex := strings.Index(contextBody, "간결한 설계")
	promptTextIndex := strings.LastIndex(contextBody, event.Prompt)
	if !(visibleContextTextIndex < memoryTextIndex && memoryTextIndex < promptTextIndex) {
		t.Fatalf("expected visible context before memory before prompt, got %q", contextBody)
	}
}

func TestVisibleContextSeparatesCurrentAndPreviousAttachments(t *testing.T) {
	visibleContext := VisibleContext{
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "current-file",
			MessageID:   "current-post",
			Path:        "~/inbox/mattermost/current.html",
			IsAvailable: true,
		}},
		Materials: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "current-file",
			MessageID:   "current-post",
			Path:        "~/inbox/mattermost/current.html",
			IsAvailable: true,
		}, {
			Platform:    "mattermost",
			FileID:      "previous-file",
			MessageID:   "previous-post",
			Path:        "~/inbox/mattermost/previous.html",
			IsAvailable: true,
		}},
	}

	agentContext := visibleContext.ToAgentVisibleContext()

	if len(agentContext.CurrentMaterials) != 1 || agentContext.CurrentMaterials[0].MaterialID != "mattermost:current-file" {
		t.Fatalf("expected current attachment to stay current, got %+v", agentContext.CurrentMaterials)
	}
	if len(agentContext.Materials) != 1 || agentContext.Materials[0].MaterialID != "mattermost:previous-file" {
		t.Fatalf("expected previous attachments to exclude current, got %+v", agentContext.Materials)
	}
}

func TestConnectorRuntimeAddsImportedImageAttachmentCatalog(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "이미지 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputParts: []agentcontract.AgentPart{{
			Type: agentcontract.AgentPartTypeImage,
			Image: &agentcontract.AgentImagePart{
				MimeType:   "image/png",
				DataBase64: "aW1hZ2U=",
				Filename:   "mascot.png",
			},
			Source: agentcontract.AgentPartSource{
				Platform:  "mattermost",
				FileID:    "file-1",
				MessageID: "message-1",
			},
		}},
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "file-1",
			URL:         "https://mattermost.local/api/v4/files/file-1",
			MessageID:   "message-1",
			Filename:    "mascot.png",
			ContentType: "image/png",
			Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/mascot.png",
			IsAvailable: true,
		}},
	}
	event := testInboundEvent("message-1")
	event.Context.ConversationType = "D"
	event.Context.InputAttachments = []InputAttachment{{Platform: "mattermost", FileID: "file-1", URL: "https://mattermost.local/api/v4/files/file-1", MessageID: "message-1"}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("expected one attachment import request, got %+v", adapter.inputAttachmentImportRequests)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	for _, expected := range []string{"Current attachments", "url=https://mattermost.local/api/v4/files/file-1", "path=~/inbox/mattermost/direct-1/message-1/mascot.png", "availableTools=read"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected attachment catalog %q in model request, got %s", expected, body)
		}
	}
	for _, unexpected := range []string{"filename=mascot.png", "contentType=image/png"} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("expected normal attachment catalog to omit %q, got %s", unexpected, body)
		}
	}
	if !connectorMessagesContainImagePart(languageModel.request.Messages, "image/png", "aW1hZ2U=") {
		t.Fatalf("expected current input image part, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeKeepsHistoryImageAttachmentCatalogOnly(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "이미지 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputParts: []agentcontract.AgentPart{{
			Type: agentcontract.AgentPartTypeImage,
			Image: &agentcontract.AgentImagePart{
				MimeType:   "image/png",
				DataBase64: "aW1hZ2U=",
				Filename:   "mascot.png",
			},
			Source: agentcontract.AgentPartSource{
				Platform:  "mattermost",
				FileID:    "file-1",
				MessageID: "message-1",
			},
		}},
	}
	event := testInboundEvent("message-2")
	event.Context.ConversationType = "D"
	event.Context.Materials = []InputAttachment{{
		Platform:    "mattermost",
		FileID:      "file-1",
		URL:         "https://mattermost.local/api/v4/files/file-1",
		MessageID:   "message-1",
		Filename:    "mascot.png",
		ContentType: "image/png",
		Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/mascot.png",
		IsAvailable: true,
	}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	if !strings.Contains(body, "url=https://mattermost.local/api/v4/files/file-1") {
		t.Fatalf("expected the history attachment's url in model request, got %s", body)
	}
	if connectorMessagesContainImagePart(languageModel.request.Messages, "image/png", "aW1hZ2U=") {
		t.Fatalf("expected history attachment catalog only, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeAddsDocumentAttachmentCatalog(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "파일 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "file-1",
			URL:         "https://mattermost.local/api/v4/files/file-1",
			MessageID:   "message-1",
			Filename:    "report.pdf",
			ContentType: "application/pdf",
			Path:        "/workspace/circles/member/inbox/mattermost/direct-1/message-1/report.pdf",
			IsAvailable: true,
		}},
	}
	event := testInboundEvent("message-1")
	event.Context.InputAttachments = []InputAttachment{{Platform: "mattermost", FileID: "file-1", URL: "https://mattermost.local/api/v4/files/file-1", MessageID: "message-1"}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	for _, expected := range []string{"Current attachments", "url=https://mattermost.local/api/v4/files/file-1", "path=/workspace/circles/member/inbox/mattermost/direct-1/message-1/report.pdf", "availableTools=read"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected attachment catalog %q in model request, got %s", expected, body)
		}
	}
	for _, unexpected := range []string{"filename=report.pdf", "contentType=application/pdf"} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("expected normal attachment catalog to omit %q, got %s", unexpected, body)
		}
	}
	if strings.Contains(body, "Markdown preview:") || strings.Contains(body, "Converted content") {
		t.Fatalf("expected no automatic document rehydrate, got %s", body)
	}
}

func TestConnectorRuntimeAddsUnavailableAttachmentCatalog(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "파일 메타데이터 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	adapter.inputAttachmentImportResult = InputAttachmentImportResult{
		InputAttachments: []InputAttachment{{
			Platform:    "mattermost",
			FileID:      "file-1",
			MessageID:   "message-1",
			Filename:    "archive.bin",
			ContentType: "application/octet-stream",
			Path:        "/workspace/circles/member/inbox/mattermost/direct-1/message-1/archive.bin",
			IsAvailable: false,
			ErrorCode:   "download_failed",
			Message:     "unsupported format",
		}},
	}
	event := testInboundEvent("message-1")
	event.Context.InputAttachments = []InputAttachment{{Platform: "mattermost", FileID: "file-1", MessageID: "message-1"}}

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}
	body := joinConnectorMessageContent(languageModel.request.Messages)
	for _, expected := range []string{"archive.bin", "application/octet-stream", "available=false", "errorCode=download_failed", "unsupported format"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected unsupported file metadata %q in model request, got %s", expected, body)
		}
	}
}

func TestConnectorAttachmentMaterialResolverImportsHistoryMaterial(t *testing.T) {
	adapter := &testAdapter{
		historyContext: VisibleContext{
			Materials: []InputAttachment{{
				Platform:    "mattermost",
				FileID:      "file-1",
				MessageID:   "root-message",
				Filename:    "mascot.png",
				ContentType: "image/png",
				SizeBytes:   5,
			}},
		},
		inputAttachmentImportResult: InputAttachmentImportResult{
			InputAttachments: []InputAttachment{{
				Platform:    "mattermost",
				FileID:      "file-1",
				MessageID:   "root-message",
				Filename:    "mascot.png",
				ContentType: "image/png",
				SizeBytes:   5,
				Path:        "/workspace/circles/member/inbox/mattermost/thread-1/mascot.png",
				IsAvailable: true,
			}},
		},
	}
	event := testInboundEvent("reply-message")
	event.Platform = "mattermost"
	event.ConversationID = "thread-1"
	event.Context.HistoryCursor = "history-cursor"
	event.Context.ConversationType = "O"
	event.Context.ChannelID = "town-square"
	resolver := connectorAttachmentMaterialResolver{adapter: adapter, personID: "person-1", event: event}

	material, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), "mattermost:file-1")

	if errorValue != nil {
		t.Fatalf("expected history material to resolve: %v", errorValue)
	}
	if material.MaterialID != "mattermost:file-1" || material.Path != "/workspace/circles/member/inbox/mattermost/thread-1/mascot.png" {
		t.Fatalf("expected imported history material, got %+v", material)
	}
	if len(adapter.historyCursors) != 1 || adapter.historyCursors[0] != "history-cursor" {
		t.Fatalf("expected resolver to fetch history, got %+v", adapter.historyCursors)
	}
	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("expected one import request, got %+v", adapter.inputAttachmentImportRequests)
	}
	importRequest := adapter.inputAttachmentImportRequests[0]
	if importRequest.MessageID != "root-message" {
		t.Fatalf("expected import to carry the source message id, got %+v", importRequest)
	}
	if !strings.Contains(importRequest.TargetDirectoryPath, "/inbox/mattermost/thread-1") || strings.Contains(importRequest.TargetDirectoryPath, "/root-message") {
		t.Fatalf("expected import to use the conversation directory without a per-message folder, got %+v", importRequest)
	}
}

func testAttachmentHistoryPage(nextHistoryCursor string, attachments ...InputAttachment) VisibleContext {
	return VisibleContext{
		Messages:      []VisibleContextMessage{{Speaker: "admin", Text: "older message"}},
		Materials:     attachments,
		HasMoreBefore: nextHistoryCursor != "",
		HistoryCursor: nextHistoryCursor,
	}
}

func TestConnectorAttachmentMaterialResolverPagesHistoryUntilTheAttachmentURLMatches(t *testing.T) {
	adapter := &testAdapter{
		historyPagesByCursor: map[string]VisibleContext{
			"page-1": testAttachmentHistoryPage("page-2"),
			"page-2": testAttachmentHistoryPage("page-3"),
			"page-3": testAttachmentHistoryPage("", InputAttachment{
				Platform:    "mattermost",
				FileID:      "file-3",
				MessageID:   "old-message",
				Filename:    "mascot.png",
				ContentType: "image/png",
				SizeBytes:   5,
				URL:         "https://mattermost.example.com/files/file-3",
			}),
		},
		inputAttachmentImportResult: InputAttachmentImportResult{
			InputAttachments: []InputAttachment{{
				Platform:    "mattermost",
				FileID:      "file-3",
				MessageID:   "old-message",
				Filename:    "mascot.png",
				ContentType: "image/png",
				SizeBytes:   5,
				Path:        "/workspace/circles/member/inbox/mattermost/thread-1/mascot.png",
				IsAvailable: true,
			}},
		},
	}
	event := testInboundEvent("reply-message")
	event.Platform = "mattermost"
	event.ConversationID = "thread-1"
	event.Context.HistoryCursor = "page-1"
	event.Context.HasMoreBefore = true
	event.Context.ConversationType = "O"
	event.Context.ChannelID = "town-square"
	resolver := connectorAttachmentMaterialResolver{adapter: adapter, personID: "person-1", event: event}

	material, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), "https://mattermost.example.com/files/file-3")

	if errorValue != nil {
		t.Fatalf("expected the third history page to resolve the attachment: %v", errorValue)
	}
	if material.Path != "/workspace/circles/member/inbox/mattermost/thread-1/mascot.png" {
		t.Fatalf("expected imported material from the third page, got %+v", material)
	}
	expectedCursors := []string{"page-1", "page-2", "page-3"}
	if len(adapter.historyCursors) != len(expectedCursors) {
		t.Fatalf("expected three history pages, got %+v", adapter.historyCursors)
	}
	for index, expectedCursor := range expectedCursors {
		if adapter.historyCursors[index] != expectedCursor {
			t.Fatalf("expected history cursors %+v, got %+v", expectedCursors, adapter.historyCursors)
		}
	}
}

func TestConnectorAttachmentMaterialResolverStopsWhenTheHistoryCursorDoesNotAdvance(t *testing.T) {
	stalledPage := testAttachmentHistoryPage("page-1")
	adapter := &testAdapter{historyPagesByCursor: map[string]VisibleContext{"page-1": stalledPage}}
	event := testInboundEvent("reply-message")
	event.Platform = "mattermost"
	event.ConversationID = "thread-1"
	event.Context.HistoryCursor = "page-1"
	event.Context.HasMoreBefore = true
	resolver := connectorAttachmentMaterialResolver{adapter: adapter, personID: "person-1", event: event}

	_, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), "https://mattermost.example.com/files/missing")

	if errorValue == nil || !strings.Contains(errorValue.Error(), "not visible in this conversation") {
		t.Fatalf("expected a not-found result, got %v", errorValue)
	}
	if len(adapter.historyCursors) != 1 {
		t.Fatalf("expected the stalled cursor to stop the lookup after one page, got %+v", adapter.historyCursors)
	}
}

func TestConnectorAttachmentMaterialResolverFailsWhenHistoryOutrunsThePageLimit(t *testing.T) {
	pages := map[string]VisibleContext{}
	for pageNumber := 1; pageNumber <= attachmentHistoryPageLimit+5; pageNumber++ {
		pages["page-"+strconv.Itoa(pageNumber)] = testAttachmentHistoryPage("page-" + strconv.Itoa(pageNumber+1))
	}
	adapter := &testAdapter{historyPagesByCursor: pages}
	event := testInboundEvent("reply-message")
	event.Platform = "mattermost"
	event.ConversationID = "thread-1"
	event.Context.HistoryCursor = "page-1"
	event.Context.HasMoreBefore = true
	resolver := connectorAttachmentMaterialResolver{adapter: adapter, personID: "person-1", event: event}

	_, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), "https://mattermost.example.com/files/missing")

	if errorValue == nil || !strings.Contains(errorValue.Error(), "attachment lookup stopped after 40 pages") {
		t.Fatalf("expected an explicit page limit failure, got %v", errorValue)
	}
	if len(adapter.historyCursors) != attachmentHistoryPageLimit {
		t.Fatalf("expected %d history pages, got %d", attachmentHistoryPageLimit, len(adapter.historyCursors))
	}
}

func TestConnectorAttachmentMaterialResolverRefreshesStaleMaterialPath(t *testing.T) {
	adapter := &testAdapter{
		inputAttachmentImportResult: InputAttachmentImportResult{
			InputAttachments: []InputAttachment{{
				Platform:    "mattermost",
				FileID:      "file-1",
				MessageID:   "message-1",
				Filename:    "report.html",
				ContentType: "text/html",
				Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/report.html",
				IsAvailable: true,
			}},
		},
	}
	event := testInboundEvent("message-1")
	event.Platform = "mattermost"
	event.ConversationID = "direct-1"
	event.Context.InputAttachments = []InputAttachment{{
		Platform:    "mattermost",
		FileID:      "file-1",
		MessageID:   "message-1",
		Filename:    "report.html",
		ContentType: "text/html",
		Path:        "/workspace/private/people/person-1/inbox/mattermost/direct-1/old/report.html",
		IsAvailable: true,
	}}
	resolver := connectorAttachmentMaterialResolver{adapter: adapter, personID: "person-1", event: event}

	material, errorValue := resolver.ResolveAttachmentMaterial(context.Background(), "mattermost:file-1")

	if errorValue != nil {
		t.Fatalf("expected stale material to refresh: %v", errorValue)
	}
	if material.Path != "/workspace/private/people/person-1/inbox/mattermost/direct-1/message-1/report.html" {
		t.Fatalf("expected refreshed material path, got %+v", material)
	}
	if len(adapter.inputAttachmentImportRequests) != 1 {
		t.Fatalf("expected one refresh import request, got %+v", adapter.inputAttachmentImportRequests)
	}
}

func TestConnectorRuntimeFetchesInitialVisibleContextFromHistoryCursor(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "맥락 확인"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true
	event.Context.HistoryCursor = "cursor-1"

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if len(adapter.historyCursors) != 1 || adapter.historyCursors[0] != "cursor-1" {
		t.Fatalf("expected initial history fetch, got %+v", adapter.historyCursors)
	}
	if !structuredMessagesContain(languageModel.request.Messages, "admin: older message") {
		t.Fatalf("expected fetched visible context in model messages, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeRunsAgentHistoryToolAndSendsOneFinishMessage(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ActionResponses: []string{
			`{"action":"continue","toolName":"conversation_history","toolInput":{"limit":20}}`,
			connectorFinishMessageWithEvidence("이전 대화를 확인했습니다", "obs-001", "conversation_history", 0),
		},
		DefaultResponsesBySchema: map[string]string{
			"bluecollar_turn_router": `{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"scripted test default","userFacingReply":""}`,
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true
	event.Context.HistoryCursor = "cursor-1"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected event to process: %v", errorValue)
	}

	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(adapter.historyCursors) != 2 || adapter.historyCursors[0] != "cursor-1" || adapter.historyCursors[1] != "cursor-1" {
		t.Fatalf("expected history fetch with cursor, got %+v", adapter.historyCursors)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one final reply, got %d", len(adapter.sentReplies))
	}
	if adapter.sentReplies[0].message != "이전 대화를 확인했습니다" {
		t.Fatalf("expected final reply, got %q", adapter.sentReplies[0].message)
	}
}

func TestConnectorRuntimeCreatesScheduledTaskFromNaturalLanguagePrompt(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ActionResponses: []string{
			`{"action":"continue","toolName":"schedule_create","toolInput":{"name":"daily research brief","taskInstruction":"업계 뉴스를 조사해서 핵심만 보고해줘.","kind":"cron","cronExpression":"0 7 * * *","repeatPolicy":"unbounded","timeZone":"Asia/Seoul"},"executionStateUpdate":{},"nextStepPlan":{"objective":"confirm schedule creation","expectedTools":[],"doneCriteria":["schedule is created"],"risk":"","workingSetReason":"schedule_create returns the created schedule"}}`,
			connectorFinishMessage("매일 아침 7시에 조사해서 알려드릴게요."),
		},
		DefaultResponsesBySchema: map[string]string{
			"bluecollar_turn_router": `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"scripted test default","userFacingReply":""}`,
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "schedule_create"})
	useTestConnectorSkill(connectorRuntime, connectorScheduledTaskSkill())
	repository := &connectorTaskScheduleRepository{}
	connectorRuntime.UseTaskScheduleRepository(repository)
	event := testInboundEvent("message-1")
	event.Prompt = "매일 업계 뉴스를 조사해서 아침 7시에 알려줘."

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil {
		t.Fatalf("expected schedule request to process: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	if len(repository.taskSchedules) != 1 {
		t.Fatalf("expected one task schedule, got %+v", repository.taskSchedules)
	}
	taskSchedule := repository.taskSchedules[0]
	if taskSchedule.Prompt != "업계 뉴스를 조사해서 핵심만 보고해줘." {
		t.Fatalf("expected stored task instruction without cadence, got %q", taskSchedule.Prompt)
	}
	if taskSchedule.CronExpression != "0 7 * * *" || taskSchedule.TimeZone != "Asia/Seoul" {
		t.Fatalf("expected cron schedule in Asia/Seoul, got %+v", taskSchedule)
	}
	if taskSchedule.Platform != event.Platform || taskSchedule.ConversationID != event.ConversationID || taskSchedule.ReplyTargetID != event.ReplyTargetID {
		t.Fatalf("expected connector context delivery target, got %+v", taskSchedule)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "매일 아침 7시에 조사해서 알려드릴게요." {
		t.Fatalf("expected confirmation reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeClassifiesConfirmationReplyBeforeResumingPendingTask(t *testing.T) {
	invokedTools := []string{}
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":""}`,
				`{"route":"continue_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"approved calendar tool work","userFacingReply":"","approval":"approve"}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다. 이미 사용자가 확인했습니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"event_delete","toolInput":{"eventHint":"event-1","userConfirmed":true}}`,
			connectorFinishMessageWithEvidence("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-002", "event_delete", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "ask_confirm", "event_add", "event_delete"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/capabilities" {
				return testCapabilityRegistrySelfHealResponse(), nil
			}
			invokedTools = append(invokedTools, strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"event_add", "event_delete"})
	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"

	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" {
		t.Fatal("expected first task run id")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다." {
		t.Fatalf("expected confirmation reply, got %+v", adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "응 맞아 삭제해"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected approval reply to process: %v", errorValue)
	}

	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.TaskRunID == "" {
		t.Fatalf("expected approved continuation to reuse task, got first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	requests := languageModel.Requests()
	approvalRouterIndex := connectorSchemaIndexAfter(requests, "bluecollar_turn_router", 1)
	if approvalRouterIndex < 0 {
		t.Fatalf("expected approval classification before continuation turn, got requests: %+v", connectorRequestSchemaNames(requests))
	}
	actionIndex := connectorSchemaIndexAfter(requests, "bluecollar_agent_turn_action", approvalRouterIndex)
	if actionIndex < 0 {
		t.Fatalf("expected continuation action after approval classification, got requests: %+v", connectorRequestSchemaNames(requests))
	}
	if !structuredMessagesContain(requests[actionIndex].Messages, "The user approved the pending action") {
		t.Fatalf("expected active goal context to carry approval context, got %+v", requests[actionIndex].Messages)
	}
	if len(invokedTools) != 1 || invokedTools[0] != "event_delete/invoke" {
		t.Fatalf("expected calendar delete tool invocation, got %+v", invokedTools)
	}
	if len(adapter.sentReplies) != 2 || adapter.sentReplies[1].message != "내일 휴가 일정을 캘린더에서 삭제했습니다." {
		t.Fatalf("expected final approved reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeClassifiesNaturalLanguageConfirmationRejection(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ChatResponsesBySchema: map[string][]string{
			"blueclaw_reply": {"알겠습니다. 이번에는 삭제하지 않겠습니다."},
		},
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":""}`,
				`{"route":"consume","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","requestedOutputFormats":null,"responseLanguage":"ko","reason":"user rejected the pending action","userFacingReply":"","approval":"reject"}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"내일 휴가 일정을 삭제할까요?"}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"event_delete","toolInput":{"eventHint":"event-1"}}`,
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
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
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"event_delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected confirmation request: %v", errorValue)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "아니, 이번에는 하지 마"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected natural-language rejection: %v", errorValue)
	}
	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.Reason != "confirmation_rejected" {
		t.Fatalf("expected pending confirmation rejection, got %+v", secondResult)
	}
	requests := languageModel.Requests()
	routerIndex := connectorSchemaIndexAfter(requests, "bluecollar_turn_router", 1)
	if routerIndex < 0 || !structuredMessagesContain(requests[routerIndex].Messages, "아니, 이번에는 하지 마") {
		t.Fatalf("expected exact rejection text in router request, got %+v", requests)
	}
	if connectorSchemaIndexAfter(requests, "bluecollar_agent_turn_action", routerIndex) >= 0 {
		t.Fatalf("expected rejection not to execute an agent action, got %+v", connectorRequestSchemaNames(requests))
	}
}

func TestConnectorRuntimeRoutesShortConfirmationReplyThroughRouter(t *testing.T) {
	invokedTools := []string{}
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":""}`,
				`{"route":"continue_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"approved calendar tool work","userFacingReply":"","approval":"approve"}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다. 이미 사용자가 확인했습니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"event_delete","toolInput":{"eventHint":"event-1","userConfirmed":true}}`,
			connectorFinishMessageWithEvidence("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-002", "event_delete", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "ask_confirm", "event_add", "event_delete"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/capabilities" {
				return testCapabilityRegistrySelfHealResponse(), nil
			}
			invokedTools = append(invokedTools, strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"event_add", "event_delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "확인"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected short approval reply to process: %v", errorValue)
	}

	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.TaskRunID == "" {
		t.Fatalf("expected approved continuation to reuse task, got first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	if connectorSchemaIndexAfter(languageModel.Requests(), "bluecollar_turn_router", 1) < 0 {
		t.Fatalf("expected short reply to route through the turn router, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if len(invokedTools) != 1 || invokedTools[0] != "event_delete/invoke" {
		t.Fatalf("expected calendar delete tool invocation, got %+v", invokedTools)
	}
	if len(adapter.sentReplies) != 2 || adapter.sentReplies[1].message != "내일 휴가 일정을 캘린더에서 삭제했습니다." {
		t.Fatalf("expected final approved reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeAnswersPendingConfirmationQuestionWithoutLaunching(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ChatResponsesBySchema: map[string][]string{
			"blueclaw_reply": {"요청하신 작업은 취소했습니다."},
		},
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":""}`,
				`{"route":"answer_question","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"user asked a follow-up instead of approving","userFacingReply":""}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"event_delete","toolInput":{"eventHint":"event-1"}}`,
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
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
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"event_delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected confirmation request, result=%+v replies=%+v", firstResult, adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "왜 승인이 필요해?"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected pending confirmation reply to process: %v", errorValue)
	}
	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.Reason != "confirmation_question" {
		t.Fatalf("expected pending confirmation question to be answered after cancelling pending action, got %+v", secondResult)
	}
	requests := languageModel.Requests()
	if connectorSchemaIndexAfter(requests, "bluecollar_agent_turn_action", connectorSchemaIndexAfter(requests, "bluecollar_turn_router", 1)) >= 0 {
		t.Fatalf("non-approval confirmation reply must not launch a new agent turn, got schemas=%+v", connectorRequestSchemaNames(requests))
	}
}

func TestConnectorRuntimeRoutesPendingConfirmationRevisionAsNewTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":""}`,
				`{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"expectedResults":[{"id":"final-message","type":"message","description":"삭제 대상 정정 요청 처리 결과","required":true}],"responseLanguage":"ko","reason":"user replaced the pending confirmation with a different message deletion target","userFacingReply":"","approval":"unclear"}`,
				`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"corrected deletion target runs as new bounded work","userFacingReply":""}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
				`{"originalInstruction":"가사랍시고 보낸 메시지를 삭제해줘","summary":"정정된 삭제 대상을 처리합니다.","targets":["platform message"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"정정된 삭제 대상을 처리합니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"event_delete","toolInput":{"eventHint":"event-1"}}`,
			connectorFinishMessage("정정한 삭제 요청으로 새로 처리했습니다."),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "ask_confirm", "event_delete", "message_search", "message_delete"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/capabilities" {
				return testCapabilityRegistrySelfHealResponse(), nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"event_delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected confirmation request, result=%+v replies=%+v", firstResult, adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "아니 가사랍시고 보낸 것들 말야"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected pending confirmation revision to process: %v", errorValue)
	}
	if secondResult.TaskRunID == "" || secondResult.TaskRunID == firstResult.TaskRunID {
		t.Fatalf("expected corrected request to launch a replacement task, got first=%q second=%q result=%+v", firstResult.TaskRunID, secondResult.TaskRunID, secondResult)
	}
	if !connectorContainsSchemaName(languageModel.Requests(), "bluecollar_agent_turn_action") {
		t.Fatalf("expected corrected request to launch agent turn, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if len(adapter.sentReplies) != 2 || adapter.sentReplies[1].message != "정정한 삭제 요청으로 새로 처리했습니다." {
		t.Fatalf("expected replacement task final reply only, got %+v", adapter.sentReplies)
	}
}

func TestAskReplyConsumesInputRevision(t *testing.T) {
	interaction := AskInteraction{
		Kind: "ask_input",
		Options: []AskChoiceOption{
			{Key: "one", Label: "선택지 1"},
			{Key: "two", Label: "선택지 2"},
		},
	}
	event := testInboundEvent("message-2")
	event.Prompt = "아니 새로 이걸 해줘"
	decision := agentcontract.TurnDecision{
		Route:          agentcontract.TurnRouteStartTask,
		Classification: agentcontract.IntakeClassificationBoundedTask,
		TaskShape:      agentcontract.TaskShapeMaintenanceTask,
		Choices:        nil,
	}

	if !askReplyConsumesInteraction(interaction, "선택지를 골라주세요", event, decision, true) {
		t.Fatal("expected non-choice replacement request to resolve the pending choice")
	}
}

func TestConnectorRuntimeInteractiveConfirmRestoresPersistedIntakeState(t *testing.T) {
	invokedTools := []string{}
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar delete needs approval first","userFacingReply":""}`,
				`{"route":"continue_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"approved calendar tool work","userFacingReply":"","approval":"approve"}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"내일 휴가 일정을 캘린더에서 삭제해줘","summary":"내일 휴가 일정을 삭제합니다.","targets":["calendar event"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":false,"thirdPartyExternalSend":false,"repeated":false,"highFrequency":false,"destructive":true,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":[],"continuationInstruction":"내일 휴가 일정을 캘린더에서 삭제합니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"내일 휴가 일정을 캘린더에서 삭제하는 것으로 이해했습니다. 승인하면 바로 진행하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"event_delete","toolInput":{"eventHint":"event-1","userConfirmed":true}}`,
			connectorFinishMessageWithEvidence("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-002", "event_delete", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "ask_confirm", "event_add", "event_delete"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/capabilities" {
				return testCapabilityRegistrySelfHealResponse(), nil
			}
			invokedTools = append(invokedTools, strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_delete","outcome":"succeeded","status":"ok","content":"calendar event deleted","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"event_add", "event_delete"})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "내일 휴가 일정을 캘린더에서 삭제해줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if !connectorTaskEventsContain(connectorRuntime, firstResult.TaskRunID, "agent.intake", `"taskShape":"approval_gated_task"`) {
		t.Fatalf("the approval turn restores what intake decided, and a task that forgets its shape asks to be approved again: %+v", connectorRuntime.taskRunService.ListTaskEvent(firstResult.TaskRunID))
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "승인할게"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected a spoken approval to resume without intake errors: %v", errorValue)
	}
	if secondResult.TaskRunID != firstResult.TaskRunID || secondResult.TaskRunID == "" {
		t.Fatalf("expected a spoken approval to reuse task, got first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	if len(invokedTools) != 1 || invokedTools[0] != "event_delete/invoke" {
		t.Fatalf("expected exactly one held calendar delete execution, got %+v", invokedTools)
	}
	if !connectorTaskEventsContain(connectorRuntime, secondResult.TaskRunID, "approval.decided", `"decision":"confirm"`) {
		t.Fatalf("a decision the ledger does not carry is a decision the approval gate cannot act on, events: %+v", connectorRuntime.taskRunService.ListTaskEvent(secondResult.TaskRunID))
	}
	if connectorSchemaIndexAfter(languageModel.Requests(), "bluecollar_turn_router", 1) < 0 {
		t.Fatalf("a spoken approval is read by the router, got %+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if len(adapter.sentReplies) != 2 || adapter.sentReplies[1].message != "내일 휴가 일정을 캘린더에서 삭제했습니다." {
		t.Fatalf("expected final approved reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeConsumesBareConfirmationReplyWithoutPendingTask(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"consume","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","requestedOutputFormats":null,"responseLanguage":"ko","reason":"orphan approval acknowledgement","userFacingReply":"","reactionEmojiName":"ok_hand"}`,
			},
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})

	event := testInboundEvent("message-approved")
	event.Prompt = "approved"
	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected orphan approval reply to process: %v", errorValue)
	}
	if result.Reason != "consume_reacted" {
		t.Fatalf("expected orphan approval to be consumed via router, got %+v", result)
	}
	if !connectorContainsSchemaName(languageModel.Requests(), "bluecollar_turn_router") {
		t.Fatalf("expected router classification, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if connectorContainsSchemaName(languageModel.Requests(), "bluecollar_agent_turn_action") {
		t.Fatalf("orphan approval must not launch an agent turn, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("orphan approval must not send a generic reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeContinuesWaitingUserInputGoal(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"business plan needs enough detail","userFacingReply":""}`,
				`{"route":"continue_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"continue active goal","userFacingReply":""}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"샘플에게 DM 보내줘","summary":"샘플에게 DM을 보냅니다.","targets":["샘플"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":true,"thirdPartyExternalSend":true,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":["보낼 메시지"],"continuationInstruction":"샘플에게 DM을 보냅니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"핵심 사업 내용을 알려주시면 더 정확히 작성하겠습니다."}`,
			},
		},
		ActionResponses: []string{
			`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"샘플","message":"우선 진행합니다."}}`,
			connectorFinishMessageWithEvidence("샘플에게 DM을 보냈습니다.", "obs-001", "message_send", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, agentcontract.SkillInstruction{
		Name:           "direct-message",
		Description:    "사업계획서 작성과 메시지 전송 후보.",
		Prompt:         "Use message_send only for explicit DM delivery.",
		ToolReferences: []string{"message_send"},
		Source:         agentcontract.InstructionSource{Path: "skills/direct-message/SKILL.md", SkillName: "direct-message"},
	})
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "ask_confirm", "message_send"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"message_send","outcome":"succeeded","status":"ok","content":"sent","result":{"messageID":"dm-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"message_send"})
	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "샘플에게 DM 보내줘"

	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}
	if firstResult.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected waiting goal reply, result=%+v replies=%+v", firstResult, adapter.sentReplies)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "우선 진행해"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected continuation to process: %v", errorValue)
	}
	if secondResult.TaskRunID != firstResult.TaskRunID {
		t.Fatalf("expected continuation to reuse waiting goal task, first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	actionRequest, isFound := connectorFirstRequestBySchema(languageModel.Requests(), "bluecollar_agent_turn_action")
	if !isFound {
		t.Fatalf("expected action request, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if !structuredMessagesContain(actionRequest.Messages, "샘플에게 DM 보내줘") {
		t.Fatalf("expected active goal original instruction in action context, got %+v", actionRequest.Messages)
	}
	if userMessageIndex(actionRequest.Messages, "우선 진행해") < 0 {
		t.Fatalf("expected latest user message to stay intact, got %+v", actionRequest.Messages)
	}
}

func TestConnectorRuntimeStartsNewTaskForClearNewRequest(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{
			"bluecollar_turn_router": {
				`{"route":"start_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"dm needs message","userFacingReply":""}`,
				`{"classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","route":"start_task","reason":"new request","userFacingReply":""}`,
			},
			"bluecollar_execution_plan": {
				`{"originalInstruction":"샘플에게 DM 보내줘","summary":"샘플에게 DM을 보냅니다.","targets":["샘플"],"schedule":"","startAt":"","endAt":"","cadence":"","externalSend":true,"thirdPartyExternalSend":true,"repeated":false,"highFrequency":false,"destructive":false,"permissionChange":false,"publicDeploy":false,"paidAction":false,"missingInformation":["보낼 메시지"],"continuationInstruction":"샘플에게 DM을 보냅니다."}`,
			},
			"blueclaw_approval_question": {
				`{"question":"보낼 메시지를 알려주세요."}`,
			},
		},
		ActionResponses: []string{
			connectorFinishMessage("캘린더 요청을 처리했습니다."),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, agentcontract.SkillInstruction{
		Name:           "direct-message",
		Description:    "DM 후보.",
		ToolReferences: []string{"message_send"},
	})

	firstEvent := testInboundEvent("message-1")
	firstEvent.Prompt = "샘플에게 DM 보내줘"
	firstResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, firstEvent)
	if errorValue != nil {
		t.Fatalf("expected first event to process: %v", errorValue)
	}

	secondEvent := testInboundEvent("message-2")
	secondEvent.Prompt = "내일 휴가 일정 캘린더에 추가해줘"
	secondResult, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, secondEvent)
	if errorValue != nil {
		t.Fatalf("expected new request to process: %v", errorValue)
	}
	if secondResult.TaskRunID == "" || secondResult.TaskRunID == firstResult.TaskRunID {
		t.Fatalf("expected clear new request to start a new task, first=%q second=%q", firstResult.TaskRunID, secondResult.TaskRunID)
	}
	actionRequest, isFound := connectorFirstRequestBySchema(languageModel.Requests(), "bluecollar_agent_turn_action")
	if !isFound {
		t.Fatalf("expected action request, got schemas=%+v", connectorRequestSchemaNames(languageModel.Requests()))
	}
	if structuredMessagesContain(actionRequest.Messages, "샘플에게 DM 보내줘") {
		t.Fatalf("expected new request not to inherit previous goal, got %+v", actionRequest.Messages)
	}
}

func TestConnectorRuntimeAddsCalendarEventWithoutApproval(t *testing.T) {
	invokedTools := []string{}
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		StructuredResponsesBySchema: map[string][]string{"bluecollar_turn_router": {
			`{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"calendar add is non-destructive tool work","userFacingReply":""}`,
		}},
		ActionResponses: []string{
			`{"action":"continue","toolName":"event_add","toolInput":{"title":"휴가","startISO":"2026-05-09","endISO":"2026-05-10","isAllDay":true}}`,
			connectorFinishMessageWithEvidence("내일 휴가 일정을 캘린더에 추가했습니다.", "obs-001", "event_add", 0),
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	useTestConnectorSkill(connectorRuntime, connectorCalendarSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "ask_confirm", "event_add", "event_delete"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/capabilities" {
				return testCapabilityRegistrySelfHealResponse(), nil
			}
			invokedTools = append(invokedTools, strings.TrimPrefix(request.URL.Path, "/v1/tools/"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"capabilityd","selectedBackend":"device","toolName":"event_add","outcome":"succeeded","status":"ok","content":"calendar event created","result":{"eventID":"event-1"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"event_add", "event_delete"})
	event := testInboundEvent("message-1")
	event.Prompt = "나 내일 휴가라고 달력에 추가해줘"

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected calendar add to process: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatal("expected task run id")
	}
	requests := languageModel.Requests()
	if connectorContainsSchemaName(requests, "bluecollar_choice_reply_decision") {
		t.Fatalf("expected no approval continuation classification, got %+v", connectorRequestSchemaNames(requests))
	}
	if len(invokedTools) != 1 || invokedTools[0] != "event_add/invoke" {
		t.Fatalf("expected direct calendar add invocation, got %+v", invokedTools)
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "내일 휴가 일정을 캘린더에 추가했습니다." {
		t.Fatalf("expected final add reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRuntimeReadsTypedCapabilityToolResponse(t *testing.T) {
	languageModel := agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ActionResponses: []string{
			`{"action":"continue","toolName":"browser_snapshot","toolInput":{},"nextStepPlan":{"objective":"observe the current browser","expectedTools":[],"expectedNextResults":["browser snapshot is available"],"doneCriteria":["snapshot result is available"],"risk":"browser may be unavailable","workingSetReason":"browser_snapshot was explicitly required"}}`,
			connectorFinishMessageWithEvidence("브라우저를 확인했습니다", "obs-001", "browser_snapshot", 0),
		},
		DefaultResponsesBySchema: map[string]string{
			"bluecollar_turn_router": `{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"scripted test default","userFacingReply":""}`,
		},
	})
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	useTestConnectorSkill(connectorRuntime, connectorBrowserSnapshotSkill())
	connectorRuntime.UseAllowedToolNames([]string{"conversation_history", "memory_search", "browser_snapshot"})
	connectorRuntime.UseTestCapabilityTools(capability.Client{
		Endpoint: "http://capability.test",
		HTTPClient: testHTTPDoer(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/capabilities" {
				return testCapabilityRegistrySelfHealResponse(), nil
			}
			if request.URL.Path != "/v1/tools/browser_snapshot/invoke" {
				t.Fatalf("unexpected capability path: %s", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"provider":"companion","selectedBackend":"device","toolName":"browser_snapshot","outcome":"succeeded","status":"ok","result":{"url":"https://example.com","snapshotText":"Example","devicePath":"/tmp/internkim-companion-files/screen.png","filename":"screen.png","contentType":"image/png","sizeBytes":123}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}, []string{"browser_snapshot"})

	event := testInboundEvent("message-1")
	event.Prompt = "open browser and observe"
	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected capability tool event to process: %v", errorValue)
	}

	requests := languageModel.Requests()
	if !structuredRequestsContainMessage(requests, "https://example.com") {
		t.Fatalf("expected typed capability result to be available as tool observation, got %+v", requests)
	}
	if adapter.sentReplies[0].message != "브라우저를 확인했습니다" {
		t.Fatalf("expected final reply, got %q", adapter.sentReplies[0].message)
	}
	if len(adapter.sentReplies[0].attachments) != 0 {
		t.Fatalf("expected observation attachment not to be delivered, got %+v", adapter.sentReplies[0].attachments)
	}
}

func structuredRequestsContainMessage(requests []llm.StructuredResponseRequest, text string) bool {
	for _, request := range requests {
		if structuredMessagesContain(request.Messages, text) {
			return true
		}
	}
	return false
}

func TestConnectorRuntimeQuarantinesSchemaOnlyMCPConfiguration(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	connectorRuntime.UseAllowedToolNames([]string{"allowed_tool"})
	mcpRegistry := mcp.NewMcpRegistry()
	inputSchema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)
	loadReport := mcpRegistry.LoadServerDefinition([]config.MCPServerConfiguration{
		{
			Name: "workspace-mcp",
			Tools: []config.MCPToolConfiguration{
				{Name: "allowed_tool", Description: "Allowed MCP tool", InputSchema: inputSchema},
				{Name: "blocked_tool", Description: "Blocked MCP tool", InputSchema: inputSchema},
			},
		},
	})
	if len(loadReport.Quarantined) != 1 {
		t.Fatalf("expected schema-only MCP server to be quarantined, got %+v", loadReport)
	}
	connectorRuntime.UseMCPRegistry(mcpRegistry)

	toolRegistry := connectorRuntime.buildTurnToolSet(adapter, testInboundEvent("message-1"), "person-1", policy.PersonAccess{})
	if _, isFound := findAgentToolDefinition(toolRegistry.ListToolDefinitions(), "allowed_tool"); isFound {
		t.Fatalf("expected quarantined MCP tools to stay hidden, got %+v", toolRegistry.ListToolDefinitions())
	}

	toolResult, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "allowed_tool", Input: json.RawMessage(`{}`)})
	if errorValue != nil {
		t.Fatalf("expected policy denial as tool result: %v", errorValue)
	}
	if !toolResult.Failed() || toolResult.ContentText() != "tool is not allowed" {
		t.Fatalf("expected quarantined MCP invocation to be denied, got %+v", toolResult)
	}
}

func TestConnectorRuntimeDetachesHTTPEventFromCanceledRequestContext(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "ok"}
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	adapter.httpParseResult = HTTPParseResult{
		HasEvent: true,
		Event:    testInboundEvent("message-http"),
	}

	result, _, errorValue := connectorRuntime.HandleHTTPEvent(request.Context(), adapter.Name(), request)
	if errorValue != nil {
		t.Fatalf("expected detached http event to process: %v", errorValue)
	}
	if result.TaskRunID == "" {
		t.Fatalf("expected task run result, got %+v", result)
	}
}

func TestConnectorRuntimeQueuesHTTPEventAndSendsReplyThroughOutbox(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "queued reply"}
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	event := testInboundEvent("message-http")
	adapter.httpParseResult = HTTPParseResult{HasEvent: true, Event: event}
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}

	result, _, errorValue := connectorRuntime.HandleHTTPEvent(context.Background(), adapter.Name(), request)
	if errorValue != nil {
		t.Fatalf("expected http event to queue: %v", errorValue)
	}
	if result.Reason != "queued" {
		t.Fatalf("expected queued result, got %+v", result)
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected no synchronous reply, got %+v", adapter.sentReplies)
	}

	if !connectorRuntime.processNextQueuedConnectorEvent(context.Background()) {
		t.Fatal("expected queued connector event to process")
	}
	if len(repository.succeededEvents) != 1 {
		t.Fatalf("expected one succeeded event, got %+v", repository.succeededEvents)
	}
	if len(repository.pendingReplies) != 1 {
		t.Fatalf("expected one queued reply, got %+v", repository.pendingReplies)
	}
	taskRunID := repository.pendingReplies[0].Reply.TaskRunID
	if taskRunID == "" || repository.pendingReplies[0].Reply.ReplyKind != connectorReplyKindSuccess {
		t.Fatalf("expected queued reply delivery metadata, got %+v", repository.pendingReplies[0].Reply)
	}
	if !connectorTaskEventsContain(connectorRuntime, taskRunID, "connector.reply.enqueued", "success") {
		t.Fatal("expected queued reply enqueue event")
	}
	if len(adapter.sentReplies) != 0 {
		t.Fatalf("expected outbox to own reply send, got %+v", adapter.sentReplies)
	}

	if !connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
		t.Fatal("expected queued connector reply to send")
	}
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "queued reply" {
		t.Fatalf("expected outbox reply to send, got %+v", adapter.sentReplies)
	}
	if len(repository.sentReplies) != 1 || repository.sentReplies[0] != "dispatch-1" {
		t.Fatalf("expected dispatch id to be recorded, got %+v", repository.sentReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, taskRunID, "connector.reply.sent", "dispatch-1") {
		t.Fatal("expected queued reply sent event")
	}
}

func TestConnectorRuntimeRecordsQueuedOutboxSendFailure(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "queued reply"}
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	adapter.sendReplyError = errors.New("mattermost send failed")
	event := testInboundEvent("message-http")
	adapter.httpParseResult = HTTPParseResult{HasEvent: true, Event: event}
	request, errorValue := http.NewRequest(http.MethodPost, "/connectors/test/events", strings.NewReader(`{}`))
	if errorValue != nil {
		t.Fatalf("expected request: %v", errorValue)
	}

	if _, _, errorValue := connectorRuntime.HandleHTTPEvent(context.Background(), adapter.Name(), request); errorValue != nil {
		t.Fatalf("expected http event to queue: %v", errorValue)
	}
	if !connectorRuntime.processNextQueuedConnectorEvent(context.Background()) {
		t.Fatal("expected queued connector event to process")
	}
	if len(repository.pendingReplies) != 1 {
		t.Fatalf("expected one queued reply, got %+v", repository.pendingReplies)
	}
	taskRunID := repository.pendingReplies[0].Reply.TaskRunID
	if !connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
		t.Fatal("expected queued connector reply attempt")
	}

	if len(repository.failedReplies) != 1 || !strings.Contains(repository.failedReplies[0], "mattermost send failed") {
		t.Fatalf("expected failed reply to be recorded, got %+v", repository.failedReplies)
	}
	if !connectorTaskEventsContain(connectorRuntime, taskRunID, "connector.reply.failed", "mattermost send failed") {
		t.Fatal("expected queued reply failed event")
	}
}

func TestConnectorRuntimeSendsCheckpointReplyKind(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	event := testInboundEvent("message-checkpoint")
	replyTarget := ReplyTarget{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID}

	errorValue := connectorRuntime.sendCheckpointReply(context.Background(), adapter.Name(), event, replyTarget, agentcontract.AgentCheckpoint{
		TaskRunID: "task-1",
		Message:   "작업 중입니다.",
		ToolName:  "shell",
	}, adapter.SendReply)
	if errorValue != nil {
		t.Fatalf("expected checkpoint reply to send: %v", errorValue)
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one sent reply, got %+v", adapter.sentReplies)
	}
	reply := adapter.sentReplies[0]
	if reply.replyKind != connectorReplyKindCheckpoint || reply.taskRunID != "task-1" || reply.message != "작업 중입니다." {
		t.Fatalf("expected checkpoint reply kind and task run id, got %+v", reply)
	}
	if !connectorTaskEventsContain(connectorRuntime, "task-1", "connector.reply.sent", connectorReplyKindCheckpoint) {
		t.Fatal("expected checkpoint sent event")
	}
}

func TestConnectorProgressHeartbeatIntervalMaintainsTypingIndicator(t *testing.T) {
	if connectorProgressHeartbeatInterval > 5*time.Second {
		t.Fatalf("expected progress heartbeat to refresh before typing expires, got %s", connectorProgressHeartbeatInterval)
	}
}

func TestConnectorRuntimeInjectsRecalledFactsAtLaunchAndNeverWritesMemoryItself(t *testing.T) {
	languageModel := &recordingLanguageModel{reply: "ok"}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	memoryRepository := seedConnectorMemory(t, "person-1", "사용자의 이름은 민수다.")
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseMemoryStore(&memory.Store{Facts: memoryRepository, Profiles: memoryRepository, Jobs: memoryRepository}, nil)
	connectorRuntime.UseTaskLauncher(connectorRuntime.routedTaskLauncherForTest(toolCatalogBuilder))

	channelEvent := testInboundEvent("message-1")
	channelEvent.ConversationID = "channel-1"
	channelEvent.Prompt = "내 이름은 민수야"
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, channelEvent); errorValue != nil {
		t.Fatalf("expected channel memory event to process: %v", errorValue)
	}
	directEvent := testInboundEvent("message-2")
	directEvent.ConversationID = "dm-1"
	directEvent.Prompt = "내 이름 뭐야?"
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, directEvent); errorValue != nil {
		t.Fatalf("expected direct memory recall event to process: %v", errorValue)
	}
	if len(memoryRepository.AllFacts()) != 1 {
		t.Fatalf("expected the connector to write no memory of its own, got %d facts", len(memoryRepository.AllFacts()))
	}
	if !structuredMessagesContain(languageModel.request.Messages, "민수") {
		t.Fatalf("expected launch-time recall to surface the stored fact, got %+v", languageModel.request.Messages)
	}
}

func TestConnectorRuntimeDoesNotShareUserMemoryWithOtherPerson(t *testing.T) {
	memoryRepository := seedConnectorMemory(t, "person-1", "사용자의 이름은 민수다.")
	hits, errorValue := memoryRepository.SearchFacts(context.Background(), memory.FactSearchQuery{
		Reader:        memory.Reader{PersonID: "person-2", SecurityLevelRank: 100, GrantedClasses: []string{"internal"}},
		Text:          "이름",
		ReferenceTime: time.Now().UTC(),
	})
	if errorValue != nil {
		t.Fatalf("expected memory search to succeed: %v", errorValue)
	}
	if len(hits) != 0 {
		t.Fatalf("expected person-2 not to read person-1 private memory, got %d", len(hits))
	}
}

func seedConnectorMemory(t *testing.T, personID string, content string) *memory.InMemoryRepository {
	t.Helper()
	memoryRepository := memory.NewInMemoryRepository()
	now := time.Now().UTC()
	episode := memory.Episode{EpisodeID: "episode-seed", SourceKind: memory.EpisodeSourceKindImport, SourceID: "seed", RequesterPersonID: personID, Content: "seed", OccurredAt: now}
	fact := memory.Fact{FactID: "fact-seed", EpisodeID: "episode-seed", ScopeType: memory.ScopeTypePrivate, ScopeID: personID, SubjectPersonID: personID, Kind: memory.FactKindIdentity, Content: content, ValidFrom: now}
	if errorValue := memoryRepository.SaveEpisode(context.Background(), memory.EpisodeWrite{Episode: episode, Facts: []memory.FactWrite{{Fact: fact}}}); errorValue != nil {
		t.Fatal(errorValue)
	}
	return memoryRepository
}

func TestConnectorRuntimeRejectsMissingHistoryCursorWhenMoreContextExists(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	event := testInboundEvent("message-1")
	event.Context.HasMoreBefore = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected malformed event to be ignored: %v", errorValue)
	}
	if !result.Ignored || result.Reason != "missing_history_cursor" {
		t.Fatalf("expected missing history cursor rejection, got %+v", result)
	}
}

func TestPlatformInboundEventOnlyUsesTextAndSenderCompatibilityAliases(t *testing.T) {
	var event PlatformInboundEvent
	errorValue := json.Unmarshal([]byte(`{
		"conversationID":"conversation-1",
		"messageID":"message-1",
		"senderUserID":"sender-1",
		"text":"hello",
		"rootMessageID":"root-1",
		"replyParentID":"parent-1"
	}`), &event)
	if errorValue != nil {
		t.Fatalf("expected compatibility event to decode: %v", errorValue)
	}

	if event.SenderID != "sender-1" {
		t.Fatalf("expected sender compatibility alias, got %q", event.SenderID)
	}
	if event.Prompt != "hello" {
		t.Fatalf("expected text compatibility alias, got %q", event.Prompt)
	}
	if event.ReplyTargetID != "" {
		t.Fatalf("expected no reply target inference, got %q", event.ReplyTargetID)
	}
}

type testAdapter struct {
	senderEmail                   string
	sendReplyError                error
	reactionError                 error
	httpParseResult               HTTPParseResult
	inputAttachmentImportResult   InputAttachmentImportResult
	inputAttachmentImportError    error
	inputAttachmentImportRequests []InputAttachmentImportRequest
	historyContext                VisibleContext
	historyPagesByCursor          map[string]VisibleContext
	sentReplies                   []testReply
	reactions                     []ReactionTarget
	removedReactions              []ReactionTarget
	progressStarts                []ReplyTarget
	progressStops                 []ReplyTarget
	progressStopErrors            []error
	historyCursors                []string
	operationNames                []string
}

type testReply struct {
	target          ReplyTarget
	message         string
	taskRunID       string
	replyKind       string
	attachments     []toolcontract.FileAttachment
	recoveryActions []toolcontract.RecoveryAction
	failureNotice   agentcontract.FailureNotice
}

type testConnectorQueueRepository struct {
	pendingEvents   []QueuedConnectorEvent
	succeededEvents []ConnectorRuntimeResult
	pendingReplies  []QueuedConnectorReply
	sentReplies     []string
	failedReplies   []string
}

type testTaskIntakeGate struct {
	isQuiesced bool
}

func (gate testTaskIntakeGate) IsQuiesced() bool {
	return gate.isQuiesced
}

type connectorTaskScheduleRepository struct {
	taskSchedules []task.TaskSchedule
}

func (repository *connectorTaskScheduleRepository) UpsertTaskSchedule(taskSchedule task.TaskSchedule) error {
	repository.taskSchedules = append(repository.taskSchedules, taskSchedule)
	return nil
}

func (repository *connectorTaskScheduleRepository) UpdateTaskSchedule(request task.TaskScheduleUpdateRequest) (task.TaskScheduleUpdateResult, error) {
	for index, taskSchedule := range repository.taskSchedules {
		if taskSchedule.TaskScheduleID != request.TaskScheduleID || taskSchedule.CreatorPersonID != request.RequesterPersonID || taskSchedule.NextRunAt == nil {
			continue
		}
		updatedTaskSchedule := taskSchedule
		var errorValue error
		if request.UpdateTaskSchedule != nil {
			updatedTaskSchedule, errorValue = request.UpdateTaskSchedule(taskSchedule)
			if errorValue != nil {
				return task.TaskScheduleUpdateResult{}, errorValue
			}
		}
		repository.taskSchedules[index] = updatedTaskSchedule
		return task.TaskScheduleUpdateResult{TaskSchedule: updatedTaskSchedule, IsFound: true}, nil
	}
	return task.TaskScheduleUpdateResult{}, nil
}

func (repository *connectorTaskScheduleRepository) ListTaskSchedules(request task.TaskScheduleListRequest) (task.TaskScheduleListResult, error) {
	taskSchedules := []task.TaskSchedule{}
	for _, taskSchedule := range repository.taskSchedules {
		if request.CreatorPersonID != "" && taskSchedule.CreatorPersonID != request.CreatorPersonID {
			continue
		}
		if !request.IncludeExpired && taskSchedule.NextRunAt == nil {
			continue
		}
		taskSchedules = append(taskSchedules, taskSchedule)
	}
	return task.TaskScheduleListResult{TaskSchedules: taskSchedules, TotalCount: len(taskSchedules), Page: 1, PageSize: len(taskSchedules)}, nil
}

func (repository *connectorTaskScheduleRepository) ClaimDueTaskSchedules(int, time.Duration, time.Time, string) ([]task.TaskSchedule, error) {
	return nil, nil
}

func (repository *connectorTaskScheduleRepository) MarkTaskScheduleSucceeded(task.TaskSchedule) error {
	return nil
}

func (repository *connectorTaskScheduleRepository) MarkTaskScheduleFailed(task.TaskSchedule, string, time.Time) error {
	return nil
}

func (repository *connectorTaskScheduleRepository) ExpireTaskSchedule(task.TaskSchedule, string, time.Time) error {
	return nil
}

func (repository *connectorTaskScheduleRepository) CancelTaskSchedules(task.TaskScheduleCancelRequest) (task.TaskScheduleCancelResult, error) {
	return task.TaskScheduleCancelResult{}, nil
}

func (repository *testConnectorQueueRepository) TryInsertConnectorEvent(PlatformInboundEvent) (bool, ConnectorRuntimeResult, error) {
	return false, ConnectorRuntimeResult{}, nil
}

func (repository *testConnectorQueueRepository) SaveConnectorResult(PlatformInboundEvent, ConnectorRuntimeResult) error {
	return nil
}

func (repository *testConnectorQueueRepository) TryEnqueueConnectorEvent(event PlatformInboundEvent) (bool, ConnectorRuntimeResult, error) {
	repository.pendingEvents = append(repository.pendingEvents, QueuedConnectorEvent{Event: event, AttemptCount: 0})
	return false, ConnectorRuntimeResult{}, nil
}

func (repository *testConnectorQueueRepository) ClaimPendingConnectorEvents(int, time.Duration) ([]QueuedConnectorEvent, error) {
	if len(repository.pendingEvents) == 0 {
		return nil, nil
	}
	queuedEvent := repository.pendingEvents[0]
	repository.pendingEvents = repository.pendingEvents[1:]
	queuedEvent.AttemptCount++
	return []QueuedConnectorEvent{queuedEvent}, nil
}

func (repository *testConnectorQueueRepository) MarkConnectorEventSucceeded(_ PlatformInboundEvent, result ConnectorRuntimeResult) error {
	repository.succeededEvents = append(repository.succeededEvents, result)
	return nil
}

func (repository *testConnectorQueueRepository) MarkConnectorEventFailed(QueuedConnectorEvent, error, time.Time) error {
	return nil
}

func (repository *testConnectorQueueRepository) EnqueueConnectorReply(event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	outboxID := event.DedupeKey()
	repository.pendingReplies = append(repository.pendingReplies, QueuedConnectorReply{
		OutboxID:     outboxID,
		RawEventID:   event.DedupeKey(),
		Platform:     event.Platform,
		ReplyTarget:  replyTarget,
		Reply:        reply,
		AttemptCount: 0,
	})
	return outboxID, nil
}

func (repository *testConnectorQueueRepository) ClaimPendingConnectorReplies(int, time.Duration) ([]QueuedConnectorReply, error) {
	if len(repository.pendingReplies) == 0 {
		return nil, nil
	}
	queuedReply := repository.pendingReplies[0]
	repository.pendingReplies = repository.pendingReplies[1:]
	queuedReply.AttemptCount++
	return []QueuedConnectorReply{queuedReply}, nil
}

func (repository *testConnectorQueueRepository) MarkConnectorReplySent(_ QueuedConnectorReply, dispatchID string) error {
	repository.sentReplies = append(repository.sentReplies, dispatchID)
	return nil
}

func (repository *testConnectorQueueRepository) MarkConnectorReplyFailed(_ QueuedConnectorReply, errorValue error, _ time.Time) error {
	repository.failedReplies = append(repository.failedReplies, errorValue.Error())
	return nil
}

func (adapter *testAdapter) Name() string {
	return "test"
}

func (adapter *testAdapter) ParseHTTPEvent(context.Context, *http.Request) (HTTPParseResult, error) {
	return adapter.httpParseResult, nil
}

func (adapter *testAdapter) ParseRealtimeEvent(context.Context, []byte, string) (PlatformInboundEvent, bool, error) {
	return PlatformInboundEvent{}, false, nil
}

func (adapter *testAdapter) ResolveIdentity(context.Context, string) (identity.PlatformAccountIdentity, error) {
	return identity.PlatformAccountIdentity{
		Platform:       adapter.Name(),
		ExternalUserID: "sender-user",
		Email:          adapter.senderEmail,
		DisplayName:    "Sender",
	}, nil
}

func (adapter *testAdapter) ImportInputAttachments(_ context.Context, request InputAttachmentImportRequest) (InputAttachmentImportResult, error) {
	adapter.inputAttachmentImportRequests = append(adapter.inputAttachmentImportRequests, request)
	if adapter.inputAttachmentImportError != nil {
		return InputAttachmentImportResult{}, adapter.inputAttachmentImportError
	}
	return adapter.inputAttachmentImportResult, nil
}

func (adapter *testAdapter) StartProgress(_ context.Context, target ReplyTarget) error {
	adapter.operationNames = append(adapter.operationNames, "progress.start")
	adapter.progressStarts = append(adapter.progressStarts, target)
	return nil
}

func (adapter *testAdapter) StopProgress(ctx context.Context, target ReplyTarget) error {
	adapter.progressStops = append(adapter.progressStops, target)
	adapter.progressStopErrors = append(adapter.progressStopErrors, ctx.Err())
	return nil
}

func (adapter *testAdapter) SendReply(_ context.Context, target ReplyTarget, reply OutboundReply) (string, error) {
	if adapter.sendReplyError != nil {
		return "", adapter.sendReplyError
	}
	adapter.sentReplies = append(adapter.sentReplies, testReply{target: target, message: reply.Message, taskRunID: reply.TaskRunID, replyKind: reply.ReplyKind, attachments: reply.Attachments, recoveryActions: reply.RecoveryActions, failureNotice: reply.FailureNotice})
	return "dispatch-" + strconv.Itoa(len(adapter.sentReplies)), nil
}

func (adapter *testAdapter) AddReaction(_ context.Context, target ReactionTarget) error {
	if adapter.reactionError != nil {
		return adapter.reactionError
	}
	adapter.reactions = append(adapter.reactions, target)
	return nil
}

func (adapter *testAdapter) RemoveReaction(_ context.Context, target ReactionTarget) error {
	adapter.removedReactions = append(adapter.removedReactions, target)
	return nil
}

func (adapter *testAdapter) FetchHistory(_ context.Context, historyCursor string, _ int) (VisibleContext, error) {
	adapter.operationNames = append(adapter.operationNames, "history.fetch")
	adapter.historyCursors = append(adapter.historyCursors, historyCursor)
	if page, isFound := adapter.historyPagesByCursor[historyCursor]; isFound {
		return page, nil
	}
	if len(adapter.historyContext.Messages) > 0 || len(adapter.historyContext.Materials) > 0 || len(adapter.historyContext.InputAttachments) > 0 {
		return adapter.historyContext, nil
	}
	return VisibleContext{
		Messages: []VisibleContextMessage{{Speaker: "admin", Text: "older message"}},
	}, nil
}

type testAdapterWithoutReaction struct {
	adapter *testAdapter
}

func (adapter testAdapterWithoutReaction) Name() string {
	return adapter.adapter.Name()
}

func (adapter testAdapterWithoutReaction) ParseHTTPEvent(ctx context.Context, request *http.Request) (HTTPParseResult, error) {
	return adapter.adapter.ParseHTTPEvent(ctx, request)
}

func (adapter testAdapterWithoutReaction) ParseRealtimeEvent(ctx context.Context, payload []byte, source string) (PlatformInboundEvent, bool, error) {
	return adapter.adapter.ParseRealtimeEvent(ctx, payload, source)
}

func (adapter testAdapterWithoutReaction) ResolveIdentity(ctx context.Context, senderID string) (identity.PlatformAccountIdentity, error) {
	return adapter.adapter.ResolveIdentity(ctx, senderID)
}

func (adapter testAdapterWithoutReaction) StartProgress(ctx context.Context, target ReplyTarget) error {
	return adapter.adapter.StartProgress(ctx, target)
}

func (adapter testAdapterWithoutReaction) StopProgress(ctx context.Context, target ReplyTarget) error {
	return adapter.adapter.StopProgress(ctx, target)
}

func (adapter testAdapterWithoutReaction) SendReply(ctx context.Context, target ReplyTarget, reply OutboundReply) (string, error) {
	return adapter.adapter.SendReply(ctx, target, reply)
}

func (adapter testAdapterWithoutReaction) FetchHistory(ctx context.Context, historyCursor string, limit int) (VisibleContext, error) {
	return adapter.adapter.FetchHistory(ctx, historyCursor, limit)
}

type testLanguageModel struct {
	reply      string
	errorValue error
}

func (languageModel testLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, languageModel.errorValue
}

func (languageModel testLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if languageModel.errorValue != nil {
		return llm.StructuredResponse{}, languageModel.errorValue
	}
	if request.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: connectorDefaultTurnRouterResponse()}, nil
	}
	return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
}

func (languageModel testLanguageModel) GenerateRecoveryChatCompletion(context.Context, llm.ChatCompletionRequest) (llm.ChatCompletionResponse, error) {
	return llm.ChatCompletionResponse{
		FinishReason:    "stop",
		SelectedBackend: "remote",
		Message:         llm.ChatCompletionMessage{Role: "assistant", Content: languageModel.reply},
	}, languageModel.errorValue
}

type blockingTestLanguageModel struct {
	reply   string
	started chan struct{}
	release chan struct{}
}

func (languageModel *blockingTestLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel *blockingTestLanguageModel) GenerateStructuredResponse(ctx context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: connectorDefaultTurnRouterResponse()}, nil
	}
	select {
	case <-languageModel.started:
	default:
		close(languageModel.started)
	}
	select {
	case <-languageModel.release:
		return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
	case <-ctx.Done():
		return llm.StructuredResponse{}, ctx.Err()
	}
}

type addressingTestLanguageModel struct {
	addressingTarget string
	dutyMatch        bool
	dutyName         string
	dutyConfidence   float64
	reactionEmoji    string
	addressingError  error
	reply            string
	requests         []llm.StructuredResponseRequest
}

func (languageModel *addressingTestLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel *addressingTestLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	if request.StructuredOutputSchema.Name == "bluecollar_addressing_classification" {
		if languageModel.addressingError != nil {
			return llm.StructuredResponse{}, languageModel.addressingError
		}
		return llm.StructuredResponse{Content: `{"target":` + strconv.Quote(languageModel.addressingTarget) + `,"shouldRespond":` + strconv.FormatBool(languageModel.addressingTarget == string(agentcontract.AddressingTargetBot)) + `,"reactionEmoji":` + strconv.Quote(languageModel.reactionEmoji) + `,"dutyMatch":` + strconv.FormatBool(languageModel.dutyMatch) + `,"dutyName":` + strconv.Quote(languageModel.dutyName) + `,"dutyConfidence":` + strconv.FormatFloat(languageModel.dutyConfidence, 'f', -1, 64) + `}`}, nil
	}
	if request.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: connectorDefaultTurnRouterResponse()}, nil
	}
	return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
}

type recordingLanguageModel struct {
	reply   string
	request llm.StructuredResponseRequest
}

func (languageModel *recordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return languageModel.reply, nil
}

func (languageModel *recordingLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if structuredResponseRequest.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: connectorDefaultTurnRouterResponse()}, nil
	}
	// A finish document parses as a verdict whose satisfied field is absent, so
	// answering the judge with one refuses every finish it grades.
	if structuredResponseRequest.StructuredOutputSchema.Name == "bluecollar_completion_judge" {
		return llm.StructuredResponse{Content: `{"satisfied":true,"missingWork":[],"reason":"recorded"}`}, nil
	}
	languageModel.request = structuredResponseRequest
	return llm.StructuredResponse{Content: connectorFinishMessage(languageModel.reply)}, nil
}

type staticScopeLanguageModel struct {
	content string
}

func (languageModel staticScopeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticScopeLanguageModel) GenerateStructuredResponse(_ context.Context, structuredResponseRequest llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if structuredResponseRequest.StructuredOutputSchema.Name == "blueclaw_graphiti_ingestion_route" {
		return llm.StructuredResponse{Content: languageModel.content}, nil
	}
	if structuredResponseRequest.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: connectorDefaultTurnRouterResponse()}, nil
	}
	return llm.StructuredResponse{Content: connectorFinishMessage("ok")}, nil
}

type testHTTPDoer func(*http.Request) (*http.Response, error)

func (doer testHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return doer(request)
}

func testCapabilityRegistrySelfHealResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"deviceCapabilities":[]}`)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func structuredMessagesContain(messages []llm.Message, fragment string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return true
		}
		for _, part := range message.Parts {
			if part.Type == "text" && strings.Contains(part.Text, fragment) {
				return true
			}
		}
	}
	return false
}

func joinConnectorMessageContent(messages []llm.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
		for _, messagePart := range message.Parts {
			if messagePart.Type == "text" {
				parts = append(parts, messagePart.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func connectorMessagesContainImagePart(messages []llm.Message, mimeType string, dataBase64 string) bool {
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Type == "image" && part.MimeType == mimeType && part.DataBase64 == dataBase64 {
				return true
			}
		}
	}
	return false
}

func messageIndex(messages []llm.Message, fragment string) int {
	for index, message := range messages {
		if strings.Contains(message.Content, fragment) {
			return index
		}
	}
	return -1
}

func userMessageIndex(messages []llm.Message, fragment string) int {
	for index, message := range messages {
		if message.Role == "user" && strings.Contains(message.Content, fragment) {
			return index
		}
	}
	return -1
}

func connectorRequestSchemaNames(requests []llm.StructuredResponseRequest) []string {
	names := []string{}
	for _, request := range requests {
		names = append(names, request.StructuredOutputSchema.Name)
	}
	return names
}

func connectorSchemaIndexAfter(requests []llm.StructuredResponseRequest, schemaName string, afterIndex int) int {
	for index := afterIndex + 1; index < len(requests); index++ {
		if requests[index].StructuredOutputSchema.Name == schemaName {
			return index
		}
	}
	return -1
}

func connectorContainsSchemaName(requests []llm.StructuredResponseRequest, schemaName string) bool {
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == schemaName {
			return true
		}
	}
	return false
}

func connectorTaskEventsContain(connectorRuntime *ConnectorRuntime, taskRunID string, name string, bodyFragment string) bool {
	for _, taskEvent := range connectorRuntime.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, bodyFragment) {
			return true
		}
	}
	return false
}

func connectorFirstRequestBySchema(requests []llm.StructuredResponseRequest, schemaName string) (llm.StructuredResponseRequest, bool) {
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == schemaName {
			return request, true
		}
	}
	return llm.StructuredResponseRequest{}, false
}

func findAgentToolDefinition(toolDefinitions []toolcontract.ToolDefinition, toolName string) (toolcontract.ToolDefinition, bool) {
	for _, toolDefinition := range toolDefinitions {
		if toolDefinition.Name == toolName {
			return toolDefinition, true
		}
	}
	return toolcontract.ToolDefinition{}, false
}

func connectorFinishMessage(reply string) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"completionSummary":` + strconv.Quote(reply) + `,"replyParts":[{"type":"text","text":` + strconv.Quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[]}`
}

func connectorDefaultTurnRouterResponse() string {
	return `{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","requestedOutputFormats":null,"responseLanguage":"ko","reason":"connector test default","userFacingReply":""}`
}

func connectorFinishMessageWithEvidence(reply string, observationID string, toolName string, attachmentIndex int) string {
	return `{"action":"finish","message":` + strconv.Quote(reply) + `,"completionSummary":` + strconv.Quote(reply) + `,"replyParts":[{"type":"text","text":` + strconv.Quote(reply) + `}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidence":[{"observationID":` + strconv.Quote(observationID) + `,"toolName":` + strconv.Quote(toolName) + `,"attachmentIndex":` + strconv.Itoa(attachmentIndex) + `}]}`
}

func appendConnectorActiveGoal(t *testing.T, taskRunService *task.TaskRunService, taskRun task.TaskRun, activeGoal agentcontract.ActiveGoal) {
	t.Helper()

	document, errorValue := json.Marshal(activeGoal)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "agent.goal.blocked", string(document))
}

func connectorRuntimeAgentKernel(connectorRuntime *ConnectorRuntime) *loop.AgentKernel {
	agentKernel, _ := connectorRuntime.harness.(*loop.AgentKernel)
	return agentKernel
}

func newTestConnectorRuntime(t *testing.T, languageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter) {
	t.Helper()

	return newTestConnectorRuntimeRoutingWith(t, languageModel, languageModel)
}

func newTestConnectorRuntimeRoutingWith(t *testing.T, languageModel llm.LanguageModelProvider, routerLanguageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter) {
	t.Helper()

	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	return connectorRuntimeForHarness(t, testConnectorAgentKernel(taskRunService, languageModel), intake.NewClassifier(languageModel), reply.NewGenerator(languageModel, nil), intake.NewTurnRouter(routerLanguageModel, agentcontract.IntakeOptions{IsEnabled: true}), taskRunService, languageModel)
}

func newStubbedTestConnectorRuntime(t *testing.T) (*ConnectorRuntime, *testAdapter, *harnesstest.Harness) {
	t.Helper()

	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	harness := harnesstest.New(taskRunService)
	connectorRuntime, adapter := connectorRuntimeForHarness(t, harness, harness, harness, harness, taskRunService, testLanguageModel{reply: "stub"})
	return connectorRuntime, adapter, harness
}

func connectorRuntimeForHarness(t *testing.T, harness agentcontract.Harness, intakeClassifier IntakeClassifier, replyGenerator ReplyGenerator, turnRouter TurnRouter, taskRunService *task.TaskRunService, languageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter) {
	t.Helper()

	connectorRuntime := NewConnectorRuntime(testConnectorIdentityService(), harness, taskRunService, task.NewTaskEventService(), nil)
	connectorRuntime.UseIntakeClassifier(intakeClassifier)
	connectorRuntime.UseReplyGenerator(replyGenerator)
	connectorRuntime.UseTaskRunService(taskRunService)
	connectorRuntime.UseTurnRouter(turnRouter)
	connectorRuntime.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, languageModel))
	testApprovalGate := approvalgate.New(taskRunService)
	testApprovalGate.UseLanguageModel(languageModel)
	connectorRuntime.UseApprovalGate(testApprovalGate)
	adapter := &testAdapter{senderEmail: "invited@example.com"}
	connectorRuntime.RegisterAdapter(adapter)
	return connectorRuntime, adapter
}

func startTaskTurnDecision() agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteStartTask,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeResearchTask,
		TaskLevel:        agentcontract.TaskLevelLow,
		ResponseLanguage: "ko",
	}
}

func testConnectorIdentityService() *identity.IdentityService {
	return identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{"invited@example.com": "person-1"},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-1": {PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal", "finance"}},
		},
	})
}

func testConnectorAgentKernel(taskRunService *task.TaskRunService, languageModel llm.LanguageModelProvider) *loop.AgentKernel {
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
	return agentKernel
}

// Connector tests only need a task run the runtime can react to, so they seed
// one the way the task service does instead of running a whole agent turn.
func seedRunningTaskRun(t *testing.T, taskRunService *task.TaskRunService, origin task.TaskRunOrigin, prompt string) task.TaskRun {
	t.Helper()

	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", origin, prompt)
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "planner")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return runningTaskRun
}

func newWaitRoutingTestConnectorRuntime(t *testing.T, languageModel llm.LanguageModelProvider) (*ConnectorRuntime, *testAdapter, *task.TaskRunService, *task.InMemoryTaskWaitTokenRepository) {
	t.Helper()

	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskWaitRepository := task.NewInMemoryTaskWaitTokenRepository()

	connectorRuntime, adapter := connectorRuntimeForHarness(t, testConnectorAgentKernel(taskRunService, languageModel), intake.NewClassifier(languageModel), reply.NewGenerator(languageModel, nil), intake.NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true}), taskRunService, languageModel)
	connectorRuntime.UseTaskWaitTokenRepository(taskWaitRepository)
	return connectorRuntime, adapter, taskRunService, taskWaitRepository
}

func createWaitingInputTaskRun(t *testing.T, taskRunService *task.TaskRunService, prompt string, interactionID string) task.TaskRun {
	t.Helper()

	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "origin-reply-target"}, prompt)
	waitingTaskRun, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingUserInput, prompt)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalConnectorEventBody(map[string]string{
		"interactionID":    interactionID,
		"kind":             "input",
		"question":         prompt,
		"message":          prompt,
		"responseLanguage": "ko",
	}))
	return waitingTaskRun
}

func createWaitingInputTaskRunWithOptions(t *testing.T, taskRunService *task.TaskRunService, prompt string, interactionID string) task.TaskRun {
	t.Helper()

	taskRun := taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "origin-reply-target"}, prompt)
	waitingTaskRun, errorValue := taskRunService.PauseTaskRun(taskRun.TaskRunID, task.TaskStatusWaitingUserInput, prompt)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	taskRunService.AppendTaskEvent(taskRun.TaskRunID, "ask.requested", marshalConnectorEventBody(map[string]any{
		"interactionID": interactionID,
		"kind":          "ask_input",
		"question":      prompt,
		"message":       prompt,
		"selectionMode": "single",
		"options": []AskChoiceOption{
			{Key: "A", Label: "웹사이트", Value: "website"},
			{Key: "B", Label: "발표자료", Value: "slides"},
		},
		"responseLanguage": "ko",
	}))
	return waitingTaskRun
}

func waitRoutingTaskWaitToken(taskRun task.TaskRun, dispatchID string, interactionID string) task.TaskWaitToken {
	now := time.Now().UTC()
	return task.TaskWaitToken{
		WaitID:         "wait-" + dispatchID,
		TaskRunID:      taskRun.TaskRunID,
		PersonID:       taskRun.RequesterPersonID,
		Platform:       "test",
		ConversationID: taskRun.OriginConversationID,
		ReplyTargetID:  dispatchID,
		ThreadRootID:   taskRun.OriginReplyTargetID,
		DispatchID:     dispatchID,
		InteractionID:  interactionID,
		Kind:           "input",
		State:          "open",
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
	}
}

func newStubbedRepositoryBackedTestConnectorRuntime(t *testing.T, taskRunRepository *testTaskRunRepository) (*ConnectorRuntime, *testAdapter, *task.TaskEventService, *harnesstest.Harness) {
	t.Helper()

	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskRunService.UseRepository(taskRunRepository)

	harness := harnesstest.New(taskRunService)
	connectorRuntime, adapter := connectorRuntimeForHarness(t, harness, harness, harness, harness, taskRunService, testLanguageModel{reply: "stub"})
	return connectorRuntime, adapter, taskEventService, harness
}

type testTaskRunRepository struct {
	taskRuns     map[string]task.TaskRun
	taskAttempts map[string]task.TaskAttempt
}

func newTestTaskRunRepository() *testTaskRunRepository {
	return &testTaskRunRepository{
		taskRuns:     map[string]task.TaskRun{},
		taskAttempts: map[string]task.TaskAttempt{},
	}
}

func (repository *testTaskRunRepository) SaveTaskRun(taskRun task.TaskRun) error {
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	return nil
}

func (repository *testTaskRunRepository) StartTaskRunAttempt(taskRun task.TaskRun, taskAttempt task.TaskAttempt) error {
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	repository.taskAttempts[taskAttempt.TaskAttemptID] = taskAttempt
	return nil
}

func (repository *testTaskRunRepository) FinishTaskRunAttempt(taskRun task.TaskRun, taskAttempt task.TaskAttempt) error {
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	if strings.TrimSpace(taskAttempt.TaskAttemptID) != "" {
		repository.taskAttempts[taskAttempt.TaskAttemptID] = taskAttempt
	}
	return nil
}

func (repository *testTaskRunRepository) TransitionTaskRun(transition task.TaskRunTransition) (task.TaskRun, error) {
	taskRun, isFound := repository.taskRuns[transition.TaskRunID]
	if !isFound {
		return task.TaskRun{}, errors.New("task run not found")
	}
	if !testTaskRunStatusAllowed(taskRun.Status, transition.FromStates) {
		return task.TaskRun{}, task.ErrIllegalTransition{
			TaskRunID:     transition.TaskRunID,
			CurrentStatus: taskRun.Status,
			FromStates:    append([]task.TaskStatus{}, transition.FromStates...),
			ToState:       transition.ToState,
		}
	}
	taskRun.Status = transition.ToState
	taskRun.UpdatedAt = transition.UpdatedAt
	if transition.StartedAttempt != nil {
		taskRun.CurrentAttemptID = transition.StartedAttempt.TaskAttemptID
		taskRun.CurrentAgentProfileName = transition.CurrentAgentProfileName
		repository.taskAttempts[transition.StartedAttempt.TaskAttemptID] = *transition.StartedAttempt
	}
	if transition.FailureReason != "" || transition.FinishCurrentAttempt {
		taskRun.FailureReason = transition.FailureReason
	}
	if transition.FinishCurrentAttempt && strings.TrimSpace(taskRun.CurrentAttemptID) != "" {
		taskAttempt := repository.taskAttempts[taskRun.CurrentAttemptID]
		taskAttempt.Status = transition.FinishedAttemptStatus
		taskAttempt.FinishedAt = &transition.UpdatedAt
		taskAttempt.FailureReason = strings.TrimSpace(transition.FailureReason)
		repository.taskAttempts[taskRun.CurrentAttemptID] = taskAttempt
	}
	repository.taskRuns[taskRun.TaskRunID] = taskRun
	return taskRun, nil
}

func testTaskRunStatusAllowed(status task.TaskStatus, allowedStatuses []task.TaskStatus) bool {
	for _, allowedStatus := range allowedStatuses {
		if status == allowedStatus {
			return true
		}
	}
	return false
}

func (repository *testTaskRunRepository) FindTaskRun(taskRunID string) (task.TaskRun, bool, error) {
	taskRun, isFound := repository.taskRuns[taskRunID]
	return taskRun, isFound, nil
}

func (repository *testTaskRunRepository) FindTaskAttempt(taskAttemptID string) (task.TaskAttempt, bool, error) {
	taskAttempt, isFound := repository.taskAttempts[taskAttemptID]
	return taskAttempt, isFound, nil
}

func (repository *testTaskRunRepository) ListTaskRun() ([]task.TaskRun, error) {
	taskRuns := make([]task.TaskRun, 0, len(repository.taskRuns))
	for _, taskRun := range repository.taskRuns {
		taskRuns = append(taskRuns, taskRun)
	}
	return taskRuns, nil
}

func (repository *testTaskRunRepository) ListTaskRunByPersonID(personID string) ([]task.TaskRun, error) {
	taskRuns := []task.TaskRun{}
	for _, taskRun := range repository.taskRuns {
		if taskRun.RequesterPersonID == personID {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns, nil
}

func (repository *testTaskRunRepository) DeleteTaskRun(string, []string) (bool, error) {
	return false, nil
}

func (repository *testTaskRunRepository) DeleteTaskRunsBefore(time.Time, []string) ([]string, error) {
	return nil, nil
}

func useTestConnectorSkill(connectorRuntime *ConnectorRuntime, skillInstruction agentcontract.SkillInstruction) {
	connectorRuntimeAgentKernel(connectorRuntime).UseSkillRetriever(loop.NewEmbeddingSkillRetriever(nil, ""))
	connectorRuntimeAgentKernel(connectorRuntime).UseInstructionBundleLoader(func() agentcontract.InstructionBundle {
		return agentcontract.InstructionBundle{Skills: []agentcontract.SkillInstruction{skillInstruction}}
	})
}

func connectorScheduledTaskSkill() agentcontract.SkillInstruction {
	return agentcontract.SkillInstruction{
		Name:           "scheduled-task",
		Description:    "Create scheduled tasks, reminders, 매일, 예약, 알림, and recurring work.",
		Prompt:         "Use schedule_create with taskInstruction for only the work to perform at run time. Put cadence and stop conditions in structured fields such as runAt, intervalSecond, cronExpression, expiresAt, and maxRunCount.",
		ToolReferences: []string{"schedule_create"},
		Source:         agentcontract.InstructionSource{Path: "skills/scheduled-task/SKILL.md", SkillName: "scheduled-task"},
	}
}

func connectorCalendarSkill() agentcontract.SkillInstruction {
	return agentcontract.SkillInstruction{
		Name:           "calendar",
		Description:    "Create or list calendar events, 일정, 달력, 캘린더, and 휴가.",
		Prompt:         "Use event_add to create calendar events without approval. Use event_delete only after approval.",
		ToolReferences: []string{"event_add", "event_delete"},
		Source:         agentcontract.InstructionSource{Path: "skills/calendar/SKILL.md", SkillName: "calendar"},
	}
}

func connectorBrowserSnapshotSkill() agentcontract.SkillInstruction {
	return agentcontract.SkillInstruction{
		Name:           "browser-snapshot",
		Description:    "Observe browser pages, snapshots, screenshots, 브라우저, and 화면 확인.",
		Prompt:         "Use browser_snapshot to observe the current browser state.",
		ToolReferences: []string{"browser_snapshot"},
		Source:         agentcontract.InstructionSource{Path: "skills/browser-snapshot/SKILL.md", SkillName: "browser-snapshot"},
	}
}

func testInboundEvent(messageID string) PlatformInboundEvent {
	return PlatformInboundEvent{
		Platform:       "test",
		Source:         "test",
		ConversationID: "direct-1",
		MessageID:      messageID,
		SenderID:       "sender-user",
		ReplyTargetID:  "reply-target-1",
		Prompt:         "hello",
	}
}

func testChannelInboundEvent(messageID string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = "channel-1"
	event.ReplyTargetID = "channel-reply-target-1"
	event.Prompt = "이거 정리해줘"
	event.Context = VisibleContext{
		ConversationType: "O",
		ChannelID:        "channel-1",
		ChannelName:      "random-chat",
	}
	return event
}

func TestResolveInboundEngagementIgnoresUninvitedAttachmentsOnly(t *testing.T) {
	connectorRuntime, _, harness := newStubbedTestConnectorRuntime(t)
	harness.AddressingDecision = agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true}

	channelEvent := PlatformInboundEvent{
		Prompt:  "User attached file(s).",
		Context: VisibleContext{ConversationType: "O", AttachmentsOnly: true},
	}
	decision := connectorRuntime.resolveInboundEngagement(context.Background(), "mattermost", channelEvent)
	if decision.ShouldLaunch {
		t.Fatalf("uninvited attachments-only channel post must be ignored, got %+v", decision)
	}
	if !strings.Contains(decision.IgnoreReason, "attachments_only") {
		t.Fatalf("expected attachments_only ignore reason, got %q", decision.IgnoreReason)
	}

	dmEvent := PlatformInboundEvent{
		Prompt:  "User attached file(s).",
		Context: VisibleContext{ConversationType: "D", AttachmentsOnly: true},
	}
	if !connectorRuntime.resolveInboundEngagement(context.Background(), "mattermost", dmEvent).ShouldLaunch {
		t.Fatal("DM with only an attachment must still engage")
	}

	mentionEvent := PlatformInboundEvent{
		Prompt:  "User attached file(s).",
		Context: VisibleContext{ConversationType: "O", AttachmentsOnly: true, Addressing: AddressingMetadata{BotMentioned: true}},
	}
	if !connectorRuntime.resolveInboundEngagement(context.Background(), "mattermost", mentionEvent).ShouldLaunch {
		t.Fatal("bot-mentioned attachment-only post must still engage")
	}
}

func TestResolveInboundEngagementReactOnly(t *testing.T) {
	connectorRuntime, _, harness := newStubbedTestConnectorRuntime(t)
	harness.AddressingDecision = agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetAnyone, ShouldRespond: false, ReactionEmoji: "eyes"}
	event := testChannelInboundEvent("message-1")

	decision := connectorRuntime.resolveInboundEngagement(context.Background(), "mattermost", event)
	if decision.ShouldLaunch {
		t.Fatalf("react-only message must not launch a task, got %+v", decision)
	}
	if decision.ReactionEmoji != "eyes" {
		t.Fatalf("expected react-only emoji 'eyes', got %+v", decision)
	}
}

func TestResolveInboundEngagementReactAndRespond(t *testing.T) {
	connectorRuntime, _, harness := newStubbedTestConnectorRuntime(t)
	harness.AddressingDecision = agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true, ReactionEmoji: "+1"}
	event := testChannelInboundEvent("message-1")

	decision := connectorRuntime.resolveInboundEngagement(context.Background(), "mattermost", event)
	if !decision.ShouldLaunch || decision.ReactionEmoji != "+1" {
		t.Fatalf("expected react-and-respond (launch + emoji), got %+v", decision)
	}
}

func TestLatestActiveGoalFailsClosedOnMalformedNewestEvent(t *testing.T) {
	validGoalDocument, _ := json.Marshal(agentcontract.ActiveGoal{
		TaskRunID: "old-task",
		Status:    agentcontract.ActiveGoalStatusActive,
	})
	taskEvents := []task.TaskEvent{
		{Name: "agent.goal.active", Body: string(validGoalDocument)},
		{Name: "agent.goal.waiting_approval", Body: `{"taskRunID":`},
	}

	activeGoal := latestActiveGoal(taskEvents)

	if strings.TrimSpace(activeGoal.RestoreError) == "" {
		t.Fatal("expected malformed newest goal to fail closed")
	}
	if activeGoal.TaskRunID != "" {
		t.Fatalf("expected no fallback to the older goal, got %+v", activeGoal)
	}
}

func TestConnectorRuntimeAcknowledgesBotMentionAndClearsAckAfterReply(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.AddressingDecision = agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true}
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "ok"}
	event := testChannelInboundEvent("message-1")
	event.Context.Addressing.BotMentioned = true

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected bot mention to process: %v", errorValue)
	}
	if result.TaskRunID == "" || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected bot mention reply, got result=%+v replies=%d", result, len(adapter.sentReplies))
	}
	if len(adapter.reactions) != 1 || adapter.reactions[0].EmojiName != "eyes" || adapter.reactions[0].Reason != "engaged_ack" {
		t.Fatalf("expected an immediate eyes acknowledgement reaction, got %+v", adapter.reactions)
	}
	if len(adapter.removedReactions) != 1 || adapter.removedReactions[0].EmojiName != "eyes" {
		t.Fatalf("expected the eyes acknowledgement to be removed after the reply, got %+v", adapter.removedReactions)
	}
}

func TestConnectorRuntimeSkipsEngagedAckForDirectMessages(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.AddressingDecision = agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true}
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "ok"}
	event := testInboundEvent("message-direct-ack")

	_, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)
	if errorValue != nil {
		t.Fatalf("expected direct message to process: %v", errorValue)
	}
	if len(adapter.reactions) != 0 {
		t.Fatalf("expected no acknowledgement reaction for a direct message, got %+v", adapter.reactions)
	}
}

func (connectorRuntime *ConnectorRuntime) routedTaskLauncherForTest(toolCatalogBuilder *agentruntime.ToolCatalogBuilder) *agentruntime.TaskLauncher {
	taskLauncher := agentruntime.NewTaskLauncher(connectorRuntime.harness, connectorRuntime.taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(connectorRuntime.turnRouter)
	taskLauncher.UseLaunchFailureCompleter(connectorRuntime.launchFailureCompleter)
	return taskLauncher
}

// The URL is the one name the model always holds for an old attachment: it
// stands in the message text long after the message left the visible window.
func TestAttachmentMaterialIsFoundByItsExactURL(t *testing.T) {
	visibleContext := VisibleContext{Messages: []VisibleContextMessage{{
		Text: "이렇게 주면? ![image](https://relay.test/media/abc.png)",
		InputAttachments: []InputAttachment{{
			Platform:    "buzz",
			URL:         "https://relay.test/media/abc.png",
			MessageID:   "root-1",
			Filename:    "image",
			ContentType: "image/png",
		}},
	}}}

	attachment, isFound := findAttachmentMaterialInContext(visibleContext, "https://relay.test/media/abc.png")
	if !isFound || attachment.MessageID != "root-1" {
		t.Fatalf("an exact URL must name its attachment, got found=%v %+v", isFound, attachment)
	}
	if _, isFound := findAttachmentMaterialInContext(visibleContext, "https://relay.test/media/other.png"); isFound {
		t.Fatal("a URL nothing carries must not resolve")
	}
	if _, isFound := findAttachmentMaterialInContext(visibleContext, ""); isFound {
		t.Fatal("an empty reference must not resolve")
	}
}
