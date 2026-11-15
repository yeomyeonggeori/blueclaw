//go:build !nobundledharness

package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const threadAskRequest = "내일 휴가 일정을 캘린더에서 삭제해줘"

type threadAskFixture struct {
	connectorRuntime *ConnectorRuntime
	adapter          *testAdapter
	through          PlatformAdapter
	languageModel    *agenttest.ScriptedLanguageModel
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
	fixture := &threadAskFixture{connectorRuntime: connectorRuntime, adapter: adapter, through: adapter, languageModel: languageModel}
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
	connectorRuntime.approvalGate.UseQuestionPoster(connectorRuntime.ThreadQuestionPoster())
	return fixture
}

func continueTaskRoute(reason string) string {
	return `{"route":"continue_task","classification":"bounded_task","taskShape":"approval_gated_task","level":"low","requestedOutputFormats":null,"responseLanguage":"ko","reason":"` + reason + `","userFacingReply":""}`
}

func (fixture *threadAskFixture) handle(t *testing.T, event PlatformInboundEvent) ConnectorRuntimeResult {
	t.Helper()
	result, errorValue := fixture.connectorRuntime.HandleInboundEvent(context.Background(), fixture.through, event)
	if errorValue != nil {
		t.Fatalf("the event failed: %v", errorValue)
	}
	return result
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

func (fixture *threadAskFixture) taskRun(t *testing.T, taskRunID string) task.TaskRun {
	t.Helper()
	taskRun, isFound := fixture.connectorRuntime.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		t.Fatalf("no task run %q", taskRunID)
	}
	return taskRun
}

func (fixture *threadAskFixture) holds(taskRunID string) []holdrecord.Hold {
	return holdrecord.Holds(fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRunID))
}

func (fixture *threadAskFixture) parkOnTheDeleteQuestion(t *testing.T) string {
	t.Helper()
	parked := fixture.handle(t, threadReplyEvent("message-1", threadAskRequest))
	if status := fixture.taskRun(t, parked.TaskRunID).Status; status != task.TaskStatusWaitingApproval {
		t.Fatalf("the run that needs an approval is %s, expected it parked", status)
	}
	return parked.TaskRunID
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
			connectorFinishMessageCiting("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-002"),
		},
	}
}

func anyTaskEventContains(taskEvents []task.TaskEvent, nameSuffix string, fragment string) bool {
	for _, taskEvent := range taskEvents {
		if strings.HasSuffix(taskEvent.Name, nameSuffix) && strings.Contains(taskEvent.Body, fragment) {
			return true
		}
	}
	return false
}

func TestAHeldCallParksTheRunAndItsQuestionIsTheOnlyMessageOfTheTurn(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())

	parked := fixture.handle(t, threadReplyEvent("message-1", threadAskRequest))

	if status := fixture.taskRun(t, parked.TaskRunID).Status; status != task.TaskStatusWaitingApproval {
		t.Fatalf("the turn ended with the run %s, expected it parked on its question", status)
	}
	holds := fixture.holds(parked.TaskRunID)
	if len(holds) != 1 || holds[0].State != holdrecord.StatePending || fixture.invokedToolCount() != 0 {
		t.Fatalf("holds %+v calls %v: expected one pending hold and no call run", holds, fixture.invokedTools)
	}
	if len(fixture.adapter.sentReplies) != 1 || fixture.questionsPosted() != 1 || fixture.adapter.sentReplies[0].message != "내일 휴가 일정을 삭제할까요?" {
		t.Fatalf("the turn sent %+v, expected the question once and nothing beside it", fixture.adapter.sentReplies)
	}
	posted, isPosted := postedRecordOf(fixture.connectorRuntime.taskRunService.ListTaskEvent(parked.TaskRunID), holds[0].ID)
	if !isPosted || posted.ConversationID != "direct-1" || posted.ReplyTargetID != "reply-target-1" || posted.MessageID != parked.ReplyDispatchID || posted.MessageID == "" {
		t.Fatalf("the ledger must say where the question was posted, got %+v for the turn %+v", posted, parked)
	}
}

func TestAReplyInTheQuestionsThreadApprovesTheCallAndResumesTheRunOnce(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript(`{"answer":"approve"}`))
	taskRunID := fixture.parkOnTheDeleteQuestion(t)

	answer := fixture.handle(t, threadReplyEvent("message-2", "ㅇ"))

	if answer.TaskRunID != taskRunID || fixture.taskRunCount() != 1 {
		t.Fatalf("the reply settled %+v among %d runs, expected the parked run alone", answer, fixture.taskRunCount())
	}
	taskRun := fixture.taskRun(t, taskRunID)
	holds := fixture.holds(taskRunID)
	if taskRun.Status != task.TaskStatusCompleted || fixture.invokedToolCount() != 1 || fixture.questionsPosted() != 1 || len(holds) != 1 || holds[0].State != holdrecord.StateSpent {
		t.Fatalf("status %s, calls %v, questions %d, holds %+v: expected a completed run that called once on its spent approval after one question", taskRun.Status, fixture.invokedTools, fixture.questionsPosted(), holds)
	}
}

func TestTheResumedRunIsHandedTheApprovedCallsResultBeforeItsModelIsAsked(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript(`{"answer":"approve"}`))
	fixture.parkOnTheDeleteQuestion(t)
	requestsBeforeTheAnswer := fixture.languageModel.RequestCount()

	fixture.handle(t, threadReplyEvent("message-2", "ㅇ"))

	firstActionRequest, isAsked := firstActionRequestSince(fixture.languageModel, requestsBeforeTheAnswer)
	if !isAsked || !strings.Contains(firstActionRequest, "calendar event deleted") {
		t.Fatalf("the resumed run's model was first asked without the approved call's result, so it would call again: %s", firstActionRequest)
	}
	if fixture.invokedToolCount() != 1 {
		t.Fatalf("the approved call ran %d times, expected once: %v", fixture.invokedToolCount(), fixture.invokedTools)
	}
}

func firstActionRequestSince(languageModel *agenttest.ScriptedLanguageModel, startIndex int) (string, bool) {
	for _, request := range languageModel.RequestsSince(startIndex) {
		if request.StructuredOutputSchema.Name != "bluecollar_agent_turn_action" {
			continue
		}
		document, _ := json.Marshal(request.Messages)
		return string(document), true
	}
	return "", false
}

func TestAReplyOutsideTheQuestionsThreadIsNotAnAnswer(t *testing.T) {
	script := deleteApprovalScript()
	script.turnRouters = append(script.turnRouters, answerQuestionRoute("a question beside the parked call"), answerQuestionRoute("a question beside the parked call"))
	script.actions = append(script.actions[:1], connectorFinishMessage("내일 휴가로 등록된 일정입니다."))
	fixture := newThreadAskFixture(t, script)
	taskRunID := fixture.parkOnTheDeleteQuestion(t)

	fixture.handle(t, rootReplyEvent("message-2", "ㅇ"))

	if holds := fixture.holds(taskRunID); len(holds) != 1 || holds[0].State != holdrecord.StatePending || fixture.invokedToolCount() != 0 {
		t.Fatalf("a reply outside the question's thread settled it: holds %+v calls %v", holds, fixture.invokedTools)
	}
}

func TestADeclineReachesTheResumedRunAsTheHeldCallsResult(t *testing.T) {
	script := deleteApprovalScript(`{"answer":"reject"}`)
	script.actions = []string{script.actions[0], connectorFinishMessage("일정을 지우지 않았습니다.")}
	fixture := newThreadAskFixture(t, script)
	taskRunID := fixture.parkOnTheDeleteQuestion(t)

	reply := fixture.handle(t, threadReplyEvent("message-2", "아니, 그건 지우지 마"))

	if reply.TaskRunID != taskRunID || fixture.taskRunCount() != 1 {
		t.Fatalf("the reply %+v belongs to the run that asked %s; it started a run of its own", reply, taskRunID)
	}
	holds := fixture.holds(taskRunID)
	if len(holds) != 1 || holds[0].State != holdrecord.StateRejected || fixture.invokedToolCount() != 0 {
		t.Fatalf("holds %+v, calls %v: expected the declined call rejected once and never run", holds, fixture.invokedTools)
	}
	if !anyTaskEventContains(fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRunID), ".result", "아니, 그건 지우지 마") {
		t.Fatal("the resumed run never learned its call was declined, or what the person said, so it would ask again")
	}
	if status := fixture.taskRun(t, taskRunID).Status; status == task.TaskStatusWaitingApproval || len(fixture.adapter.sentReplies) != 2 {
		t.Fatalf("the resumed run is %s after %d messages, expected it to end with one reply after its question", status, len(fixture.adapter.sentReplies))
	}
}

func TestAnUnrelatedRequestClosesTheHoldRunsNoCallAndStartsANewRun(t *testing.T) {
	script := deleteApprovalScript(`{"answer":"none"}`)
	script.turnRouters = append(script.turnRouters, startTaskRoute("an unrelated request"), startTaskRoute("an unrelated request"))
	script.actions = []string{script.actions[0], connectorFinishMessage("오늘 서울은 맑습니다.")}
	fixture := newThreadAskFixture(t, script)
	reader := &countingReplyReader{reading: approvalreply.Reading{}}
	fixture.connectorRuntime.UseApprovalReplyReader(reader)
	parkedRunID := fixture.parkOnTheDeleteQuestion(t)

	unrelated := fixture.handle(t, threadReplyEvent("message-2", "오늘 서울 날씨 알려줘"))

	if unrelated.TaskRunID == parkedRunID || unrelated.TaskRunID == "" || fixture.taskRunCount() != 2 {
		t.Fatalf("the unrelated request ran as %+v among %d runs, expected a run of its own", unrelated, fixture.taskRunCount())
	}
	holds := fixture.holds(parkedRunID)
	if len(holds) != 1 || holds[0].State == holdrecord.StatePending || fixture.invokedToolCount() != 0 {
		t.Fatalf("holds %+v calls %v: expected the hold closed as moved on and the held call never run", holds, fixture.invokedTools)
	}
	if status := fixture.taskRun(t, parkedRunID).Status; status != task.TaskStatusCancelled {
		t.Fatalf("the run left behind is %s, expected it closed quietly", status)
	}
	if reader.readCount != 1 {
		t.Fatalf("the reply was read %d times, expected once", reader.readCount)
	}
	for _, reply := range fixture.adapter.sentReplies {
		if reply.taskRunID == parkedRunID && reply.replyKind != connectorReplyKindApprovalQuestion {
			t.Fatalf("the run left behind sent %+v, expected it closed without a notice", reply)
		}
	}
}

func TestAReplyThatPicksNoOptionContinuesTheParkedRunToldItsCallWasNotRun(t *testing.T) {
	script := deleteApprovalScript(`{"answer":"none"}`)
	script.turnRouters = append(script.turnRouters, continueTaskRoute("the reply changes what the parked run should do"), continueTaskRoute("the reply changes what the parked run should do"))
	script.actions = []string{script.actions[0], connectorFinishMessage("모레 일정으로 다시 확인하겠습니다.")}
	fixture := newThreadAskFixture(t, script)
	parkedRunID := fixture.parkOnTheDeleteQuestion(t)

	continued := fixture.handle(t, threadReplyEvent("message-2", "아 내일 말고 모레 일정이야"))

	if continued.TaskRunID != parkedRunID || fixture.taskRunCount() != 1 {
		t.Fatalf("the reply ran as %+v among %d runs, expected the parked run to continue", continued, fixture.taskRunCount())
	}
	holds := fixture.holds(parkedRunID)
	if len(holds) != 1 || holds[0].State == holdrecord.StatePending || fixture.invokedToolCount() != 0 {
		t.Fatalf("holds %+v calls %v: expected the hold closed and the held call never run", holds, fixture.invokedTools)
	}
	if !anyTaskEventContains(fixture.connectorRuntime.taskRunService.ListTaskEvent(parkedRunID), ".result", "without choosing") {
		t.Fatal("the continued run was never told, as its held call's result, that the requester replied without choosing and the call was not run")
	}
}

func parkedRunWithItsQuestionPosted(t *testing.T, fixture *threadAskFixture) task.TaskRun {
	t.Helper()
	taskRun := parkedRunWhoseQuestionWasNeverPosted(t, fixture)
	holds := fixture.holds(taskRun.TaskRunID)
	fixture.connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, TaskEventQuestionPosted, agentruntime.MarshalBody(postedQuestionRecord{HoldID: holds[0].ID, Platform: "test", ConversationID: "direct-1", ReplyTargetID: "reply-target-1", MessageID: "dispatch-before-the-restart"}))
	return taskRun
}

func parkedRunWhoseQuestionWasNeverPosted(t *testing.T, fixture *threadAskFixture) task.TaskRun {
	t.Helper()
	taskRun := fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1", IsThread: true}, threadAskRequest)
	fixture.connectorRuntime.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentTaskLaunched, `{"platform":"test","conversationID":"direct-1","replyTargetID":"reply-target-1","sourceReference":"test:direct-1:message-0"}`)
	holdPendingCall(t, fixture.connectorRuntime, taskRun.TaskRunID)
	return fixture.taskRun(t, taskRun.TaskRunID)
}

func holdPendingCall(t *testing.T, connectorRuntime *ConnectorRuntime, taskRunID string) {
	t.Helper()
	if _, errorValue := connectorRuntime.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, "삭제할까요?"); errorValue != nil {
		t.Fatalf("the run would not wait for approval: %v", errorValue)
	}
	holdrecord.Open(connectorRuntime.taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: []byte(`{"eventHint":"event-1"}`), Confirmation: "내일 휴가 일정을 삭제할까요?"}, nil)
}

func TestASecondQuestionInTheSameThreadClosesTheFirstHold(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	older := parkedRunWithItsQuestionPosted(t, fixture)

	newer := fixture.parkOnTheDeleteQuestion(t)

	olderHolds := fixture.holds(older.TaskRunID)
	if len(olderHolds) != 1 || olderHolds[0].State == holdrecord.StatePending {
		t.Fatalf("the older question still waits beside the newer one: %+v", olderHolds)
	}
	if status := fixture.taskRun(t, older.TaskRunID).Status; status == task.TaskStatusWaitingApproval {
		t.Fatalf("two runs wait in one thread: the older one is still %s", status)
	}
	if newerHolds := fixture.holds(newer); len(newerHolds) != 1 || newerHolds[0].State != holdrecord.StatePending {
		t.Fatalf("the newer question must be the one that waits, got %+v", newerHolds)
	}
	if _, isParked := fixture.connectorRuntime.parkedRunAwaiting("person-1", threadReplyEvent("message-2", "ㅇ")); !isParked {
		t.Fatal("a reply in the thread found no question to answer")
	}
}

func TestAQuestionPostedWithNoMessageBeforeItStillClosesTheOlderHoldInItsThread(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	older := parkedRunWithItsQuestionPosted(t, fixture)
	newer := parkedRunWhoseQuestionWasNeverPosted(t, fixture)

	fixture.connectorRuntime.postUnpostedQuestions(context.Background())

	if olderHolds := fixture.holds(older.TaskRunID); len(olderHolds) != 1 || olderHolds[0].State == holdrecord.StatePending {
		t.Fatalf("the older question still waits beside the newer one: %+v", olderHolds)
	}
	if status := fixture.taskRun(t, older.TaskRunID).Status; status == task.TaskStatusWaitingApproval {
		t.Fatalf("two runs wait in one thread: the older one is still %s", status)
	}
	if newerHolds := fixture.holds(newer.TaskRunID); len(newerHolds) != 1 || newerHolds[0].State != holdrecord.StatePending {
		t.Fatalf("the newer question must be the one that waits, got %+v", newerHolds)
	}
}

func TestAQuestionInAnotherThreadLeavesTheFirstHoldWaiting(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	older := parkedRunWithItsQuestionPosted(t, fixture)
	otherThread := threadReplyEvent("message-1", threadAskRequest)
	otherThread.ReplyTargetID = "reply-target-2"

	fixture.handle(t, otherThread)

	if olderHolds := fixture.holds(older.TaskRunID); len(olderHolds) != 1 || olderHolds[0].State != holdrecord.StatePending {
		t.Fatalf("a question in another thread closed this one: %+v", olderHolds)
	}
}

func TestAQuestionNeverPostedBeforeARestartIsPostedOnceAtStart(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := parkedRunWhoseQuestionWasNeverPosted(t, fixture)

	fixture.connectorRuntime.postUnpostedQuestions(context.Background())
	fixture.connectorRuntime.postUnpostedQuestions(context.Background())

	if fixture.questionsPosted() != 1 {
		t.Fatalf("a hold whose question was never posted was asked %d times, expected once", fixture.questionsPosted())
	}
	if status := fixture.taskRun(t, taskRun.TaskRunID).Status; status != task.TaskStatusWaitingApproval {
		t.Fatalf("posting the question left the run %s, expected it still parked with nothing waiting on it", status)
	}
}

func TestAQuestionAlreadyPostedBeforeARestartIsNotPostedAgain(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	parkedRunWithItsQuestionPosted(t, fixture)

	fixture.connectorRuntime.postUnpostedQuestions(context.Background())

	if fixture.questionsPosted() != 0 {
		t.Fatalf("a question posted before the restart was posted %d more times", fixture.questionsPosted())
	}
}

func TestAnAnswerAfterARestartResumesTheRunFromTheLedgerAlone(t *testing.T) {
	script := deleteApprovalScript(`{"answer":"approve"}`)
	script.actions = []string{connectorFinishMessageCiting("내일 휴가 일정을 캘린더에서 삭제했습니다.", "obs-001")}
	fixture := newThreadAskFixture(t, script)
	taskRun := parkedRunWithItsQuestionPosted(t, fixture)

	fixture.handle(t, threadReplyEvent("message-2", "ㅇ"))

	holds := fixture.holds(taskRun.TaskRunID)
	if len(holds) != 1 || holds[0].State != holdrecord.StateSpent || fixture.invokedToolCount() != 1 || fixture.taskRun(t, taskRun.TaskRunID).Status != task.TaskStatusCompleted {
		t.Fatalf("holds %+v, calls %v, status %s: expected the approved call carried out once from the ledger on a completed run", holds, fixture.invokedTools, fixture.taskRun(t, taskRun.TaskRunID).Status)
	}
}

func TestOneReplyReadAsASessionPromptSettlesTheParkedRunOnceWithOneReading(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	reader := &countingReplyReader{reading: approvalreply.Reading{OptionID: approvalgate.RejectOptionKey}}
	fixture.connectorRuntime.UseApprovalReplyReader(reader)
	taskRun := parkedRunWithItsQuestionPosted(t, fixture)
	openTurn := func() *SessionTurn {
		return fixture.connectorRuntime.OpenSessionTurn(context.Background(), threadReplyEvent("message-2", "ㄴ"), "person-1", fixture.adapter.SendReply)
	}

	answer := openTurn().AnswerParkedQuestion(context.Background())
	again := openTurn().AnswerParkedQuestion(context.Background())

	if !answer.IsAnswer || !answer.ShouldResume || answer.TaskRun.TaskRunID != taskRun.TaskRunID || len(answer.SettledCalls) != 1 {
		t.Fatalf("the reply settled %+v, expected the parked run resumed with its declined call", answer)
	}
	if again.ShouldResume || reader.readCount != 1 {
		t.Fatalf("the same reply resumed the run again (%+v) or was read %d times", again, reader.readCount)
	}
	if holds := fixture.holds(taskRun.TaskRunID); len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
		t.Fatalf("holds %+v: expected the one hold declined once", holds)
	}
}

func TestAnAnswerAnotherReaderAlreadySettledDoesNotResumeTheRunAgain(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := parkedRunWithItsQuestionPosted(t, fixture)
	parked, _ := fixture.connectorRuntime.parkedRunOf(taskRun)
	first := fixture.connectorRuntime.settleParkedRun(context.Background(), parked, approvalgate.ApproveOptionKey, requesterAnswerSource)

	second := fixture.connectorRuntime.settleParkedRun(context.Background(), parked, approvalgate.ApproveOptionKey, requesterAnswerSource)

	if !first.ShouldResume || !second.IsAnswer || second.ShouldResume {
		t.Fatalf("first %+v second %+v: one answer read twice must resume the run once", first, second)
	}
}

func TestOnlyTheRequesterInTheQuestionsPlacementAnswersIt(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	parkedRunWithItsQuestionPosted(t, fixture)
	otherConversation := threadReplyEvent("message-2", "ㅇ")
	otherConversation.ConversationID = "channel-1"
	otherPlatform := threadReplyEvent("message-2", "ㅇ")
	otherPlatform.Platform = "other"

	if _, isParked := fixture.connectorRuntime.parkedRunAwaiting("person-2", threadReplyEvent("message-2", "ㅇ")); isParked {
		t.Fatal("another person's reply was offered the question")
	}
	if _, isParked := fixture.connectorRuntime.parkedRunAwaiting("person-1", otherConversation); isParked {
		t.Fatal("a reply in another conversation was offered the question")
	}
	if _, isParked := fixture.connectorRuntime.parkedRunAwaiting("person-1", otherPlatform); isParked {
		t.Fatal("a reply on another platform was offered the question")
	}
	if _, isParked := fixture.connectorRuntime.parkedRunAwaiting("person-1", threadReplyEvent("message-2", "ㅇ")); !isParked {
		t.Fatal("the requester's reply in the question's thread was not offered the question")
	}
}

func TestAnOperatorApprovalSettlesTheParkedQuestionThroughTheSameAnswerPoint(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := parkedRunWithItsQuestionPosted(t, fixture)

	settlement, errorValue := fixture.connectorRuntime.AnswerParkedQuestionAsOperator(context.Background(), taskRun.TaskRunID, agentcontract.ApprovalSignalApprove)

	holds := fixture.holds(taskRun.TaskRunID)
	if errorValue != nil || settlement.Signal != agentcontract.ApprovalSignalApprove || len(holds) != 1 || holds[0].State != holdrecord.StateApproved {
		t.Fatalf("settlement %+v error %v holds %+v: expected the hold approved for the resumed run to spend", settlement, errorValue, holds)
	}
	if status := fixture.taskRun(t, taskRun.TaskRunID).Status; status == task.TaskStatusWaitingApproval {
		t.Fatalf("the settled run is still %s, so a reply in the thread could settle it a second time", status)
	}
	if _, errorValue := fixture.connectorRuntime.AnswerParkedQuestionAsOperator(context.Background(), taskRun.TaskRunID, agentcontract.ApprovalSignalApprove); !errors.Is(errorValue, ErrRunIsNotParked) {
		t.Fatalf("a second operator answer must find nothing parked, got %v", errorValue)
	}
}

func TestAnOperatorRejectionIsTheHeldCallsResult(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := parkedRunWithItsQuestionPosted(t, fixture)

	settlement, errorValue := fixture.connectorRuntime.AnswerParkedQuestionAsOperator(context.Background(), taskRun.TaskRunID, agentcontract.ApprovalSignalReject)

	holds := fixture.holds(taskRun.TaskRunID)
	if errorValue != nil || len(settlement.SettledCalls()) != 1 || len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
		t.Fatalf("settlement %+v error %v holds %+v: expected the hold rejected and the call carried as not done", settlement, errorValue, holds)
	}
}

func TestAnOperatorCannotApproveAQuestionThatAsksThePersonToPick(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := fixture.connectorRuntime.taskRunService.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1", IsThread: true}, threadAskRequest)
	fixture.connectorRuntime.taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingApproval, "언제 할까요?")
	holdrecord.Open(fixture.connectorRuntime.taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{ToolName: "host_update", Confirmation: "언제 할까요?"}, []holdrecord.Choice{{Key: "now"}})

	_, errorValue := fixture.connectorRuntime.AnswerParkedQuestionAsOperator(context.Background(), taskRun.TaskRunID, agentcontract.ApprovalSignalApprove)

	if !errors.Is(errorValue, ErrHoldOffersChoices) {
		t.Fatalf("an operator approved a question only the requester can pick an option of: %v", errorValue)
	}
}

func TestARunResumedOnAHeldCallOwesThatCallSoTheLoopDoesNotRefuseItAsUnrequested(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	taskRun := parkedRunWithItsQuestionPosted(t, fixture)

	activeGoal := interruptedTaskActiveGoal(taskRun, fixture.connectorRuntime.taskRunService.ListTaskEvent(taskRun.TaskRunID), "")

	if !slices.Contains(activeGoal.OutcomeContract.RequiredEvidenceTools, "event_delete") {
		t.Fatalf("the resumed goal requires %v, and a send or delete the goal does not require is refused by the loop as unrequested", activeGoal.OutcomeContract.RequiredEvidenceTools)
	}
}

func askWhileTheMessengerFailsWith(t *testing.T, deliveryError error) (*threadAskFixture, task.TaskRun) {
	t.Helper()
	fixture := newThreadAskFixture(t, deleteApprovalScript())
	fixture.adapter.sendReplyError = deliveryError
	fixture.handle(t, threadReplyEvent("message-1", threadAskRequest))
	taskRuns := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")
	if len(taskRuns) != 1 {
		t.Fatalf("expected one run, found %d", len(taskRuns))
	}
	return fixture, taskRuns[0]
}

func TestAQuestionTheMessengerCouldNotCarryBecauseItIsDownStaysPendingToBePostedOnce(t *testing.T) {
	fixture, taskRun := askWhileTheMessengerFailsWith(t, capability.TransportError{Cause: errors.New("dial unix: connect: connection refused")})

	if taskRun.Status != task.TaskStatusWaitingApproval || connectorTaskEventsContain(fixture.connectorRuntime, taskRun.TaskRunID, approvalgate.TaskEventApprovalUnreachable, "") {
		t.Fatalf("a messenger that is down left the run %s with an unreachable verdict, expected it parked", taskRun.Status)
	}
	fixture.adapter.sendReplyError = nil
	fixture.connectorRuntime.postUnpostedQuestions(context.Background())
	if fixture.questionsPosted() != 1 {
		t.Fatalf("the question owed to the requester was posted %d times once the messenger was back, expected once", fixture.questionsPosted())
	}
}

func TestAQuestionToARequesterTheMessengerRefusesIsUnreachable(t *testing.T) {
	fixture, taskRun := askWhileTheMessengerFailsWith(t, errors.New("no direct conversation with this person"))

	if taskRun.Status == task.TaskStatusWaitingApproval || !connectorTaskEventsContain(fixture.connectorRuntime, taskRun.TaskRunID, approvalgate.TaskEventApprovalUnreachable, "") {
		t.Fatalf("a refused question left the run %s without an unreachable verdict", taskRun.Status)
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

func TestAQueuedReplyAnswersTheParkedRunLikeAnyMessage(t *testing.T) {
	fixture := newThreadAskFixture(t, deleteApprovalScript(`{"answer":"approve"}`))
	repository := &testConnectorQueueRepository{}
	fixture.connectorRuntime.UseEventRepository(repository)
	queueEvents(t, fixture, threadReplyEvent("message-1", threadAskRequest))
	fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	taskRuns := fixture.connectorRuntime.taskRunService.ListTaskRunByPersonID("person-1")
	if len(taskRuns) != 1 || taskRuns[0].Status != task.TaskStatusWaitingApproval {
		t.Fatalf("the queued request left %+v, expected one parked run and a free worker", taskRuns)
	}

	queueEvents(t, fixture, threadReplyEvent("message-2", "ㅇㅇ"))
	fixture.connectorRuntime.processNextQueuedConnectorEvent(context.Background())

	repository.mutex.Lock()
	succeeded := len(repository.succeededEvents)
	repository.mutex.Unlock()
	if status := fixture.taskRun(t, taskRuns[0].TaskRunID).Status; status != task.TaskStatusCompleted || fixture.invokedToolCount() != 1 || succeeded != 2 {
		t.Fatalf("status %s with calls %v and %d events settled, expected the parked run to finish once answered", status, fixture.invokedTools, succeeded)
	}
}
