package acpsession

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/agenttest"
	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

type countingReader struct {
	mutex     sync.Mutex
	optionID  string
	readCount int
}

func (reader *countingReader) Read(context.Context, approvalreply.Question, string, agentcontract.LLMCallObserver) (approvalreply.Reading, error) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	reader.readCount++
	return approvalreply.Reading{OptionID: reader.optionID}, nil
}

func (reader *countingReader) reads() int {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	return reader.readCount
}

type parkingPlane struct {
	taskRunService *task.TaskRunService
	gate           *approvalgate.Gate
	reader         *countingReader
	launcher       *recordingLauncher
	client         *recordingClient
	connection     *acp.ClientSideConnection
}

func aParkingPlane(t *testing.T, readOptionID string) *parkingPlane {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	plane := &parkingPlane{
		taskRunService: taskRunService,
		gate:           approvalgate.New(taskRunService),
		reader:         &countingReader{optionID: readOptionID},
		launcher:       &recordingLauncher{reply: "처리했습니다", taskRunID: "run-started-by-this-prompt"},
		client:         &recordingClient{},
	}
	connectorRuntime := connectorRuntimeForTest(taskRunService)
	connectorRuntime.RegisterAdapter(&messengerAdapter{})
	connectorRuntime.UseApprovalGate(plane.gate)
	connectorRuntime.UseApprovalReplyReader(plane.reader)
	connection, sessionRoutes := connectedPairWithCollaborators(t, plane.client, Collaborators{
		TaskLauncher: plane.launcher,
		Directory:    staticDirectory{},
		SessionTurns: connectorRuntime,
		TaskRunStore: taskRunService,
	})
	plane.gate.UseQuestionPoster(sessionRoutes)
	connectorRuntime.UseHandoffCloser(sessionRoutes)
	plane.connection = connection
	return plane
}

const parkedConversationID = "conversation-1"

func (plane *parkingPlane) heldCallRequest(taskRunID string) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: "person-sample",
		RequesterEmail:    "sample@example.test",
		TaskRunID:         taskRunID,
		Platform:          "buzz",
		ConversationID:    parkedConversationID,
		ReplyTargetID:     "thread-root",
		ToolName:          "event_delete",
		ToolInput:         json.RawMessage(`{"eventID":"event-1"}`),
	}
}

func (plane *parkingPlane) parkARun(t *testing.T) agentcontract.TaskRun {
	t.Helper()
	taskRun := plane.taskRunService.CreateTaskRunWithOrigin("person-sample", task.TaskRunOrigin{ConversationID: parkedConversationID, ReplyTargetID: "thread-root", IsThread: true}, "내일 회의 지워줘")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome, errorValue := plane.gate.AwaitApproval(ctx, plane.heldCallRequest(taskRun.TaskRunID))
	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("the held call came back %+v %v, expected the run parked", outcome, errorValue)
	}
	return taskRun
}

func (plane *parkingPlane) holds(taskRunID string) []holdrecord.Hold {
	return holdrecord.Holds(plane.taskRunService.ListTaskEvent(taskRunID))
}

func (plane *parkingPlane) replyInTheThread(t *testing.T, sessionID acp.SessionId, messageID string, words string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errorValue := plane.connection.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock(words)},
		Meta:      map[string]any{MessageMetaKey: MessageContext{MessageID: messageID, ReplyTargetID: "thread-root", IsThread: true}},
	})
	if errorValue != nil {
		t.Fatalf("prompt: %v", errorValue)
	}
}

func (plane *parkingPlane) messagesPosted() []string {
	plane.client.mutex.Lock()
	defer plane.client.mutex.Unlock()
	return append([]string{}, plane.client.messages...)
}

func (plane *parkingPlane) deliveries() []Delivery {
	plane.client.mutex.Lock()
	defer plane.client.mutex.Unlock()
	return append([]Delivery{}, plane.client.deliveries...)
}

func (plane *parkingPlane) launches() []agentruntime.TaskLaunchRequest {
	plane.launcher.mutex.Lock()
	defer plane.launcher.mutex.Unlock()
	return append([]agentruntime.TaskLaunchRequest{}, plane.launcher.launched...)
}

func TestAHeldCallReachesTheSessionAsAnOrdinaryMessageAndNeverAsAPermissionRequest(t *testing.T) {
	plane := aParkingPlane(t, "")
	openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))

	taskRun := plane.parkARun(t)

	messages, holds := plane.messagesPosted(), plane.holds(taskRun.TaskRunID)
	if len(messages) != 1 || len(holds) != 1 || messages[0] != holds[0].Call.Confirmation || plane.client.permissionRequests() != 0 {
		t.Fatalf("the session was sent %v and %d permission requests for the hold %+v, expected its question once as a message", messages, plane.client.permissionRequests(), holds)
	}
	deliveries := plane.deliveries()
	if len(deliveries) != 1 || deliveries[0].ReplyTargetID != "thread-root" || deliveries[0].DeliveryID == "" || !deliveries[0].IsFinal {
		t.Fatalf("the question's delivery was %+v, expected the last message of a delivery addressed to the thread the run answers in", deliveries)
	}
	if messageID := connectors.PostedApprovalQuestionMessageID(plane.taskRunService.ListTaskEvent(taskRun.TaskRunID)); messageID != "posted-1" {
		t.Fatalf("the ledger says the question was posted as %q, expected the message the relay reported", messageID)
	}
	if status := mustFindTaskRun(t, plane.taskRunService, taskRun.TaskRunID).Status; status != agentcontract.TaskStatusWaitingApproval {
		t.Fatalf("the run is %s, expected it parked", status)
	}
}

func mustFindTaskRun(t *testing.T, taskRunService *task.TaskRunService, taskRunID string) agentcontract.TaskRun {
	t.Helper()
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		t.Fatalf("no task run %q", taskRunID)
	}
	return taskRun
}

func TestAHandoffQuestionTellsTheRelayWhichHandoffItIs(t *testing.T) {
	plane := aParkingPlane(t, "")
	openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	taskRun := plane.taskRunService.CreateTaskRunWithOrigin("person-sample", task.TaskRunOrigin{ConversationID: parkedConversationID}, "로그인해줘")
	named := approvalgate.ApprovalTarget{InputField: "handoff", ID: "handoff-1", Title: "https://portal.example.com", HandoffID: "handoff-1"}

	if _, errorValue := plane.gate.AwaitApproval(approvalgate.WithRefusalTarget(context.Background(), named), plane.heldCallRequest(taskRun.TaskRunID)); errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}

	if deliveries := plane.deliveries(); len(deliveries) != 1 || deliveries[0].HandoffID != "handoff-1" {
		t.Fatalf("the relay cannot open a live view it was not told about, got %+v", deliveries)
	}
}

func TestACallInAConversationNoSessionOwnsIsRefusedAndNeverParked(t *testing.T) {
	plane := aParkingPlane(t, "")
	openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	taskRun := plane.taskRunService.CreateTaskRun("person-sample", "conversation-nobody-opened", "지워줘")
	request := plane.heldCallRequest(taskRun.TaskRunID)
	request.ConversationID = "conversation-nobody-opened"

	outcome, _ := plane.gate.AwaitApproval(context.Background(), request)

	if outcome.Decision != mcpserver.ApprovalDecisionUnanswerable || len(plane.messagesPosted()) != 0 {
		t.Fatalf("a call was put to a session that owns another conversation: %+v %v", outcome, plane.messagesPosted())
	}
}

func TestAQuestionTheRelayNeverReportedOnBecauseItLeftStaysPendingToBePostedOnce(t *testing.T) {
	plane := aParkingPlane(t, "")
	plane.client.isSilent = true
	openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	taskRun := plane.taskRunService.CreateTaskRun("person-sample", parkedConversationID, "지워줘")
	ctx, leave := context.WithCancel(context.Background())
	outcomes := make(chan mcpserver.ApprovalOutcome, 1)
	go func() {
		outcome, _ := plane.gate.AwaitApproval(ctx, plane.heldCallRequest(taskRun.TaskRunID))
		outcomes <- outcome
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(plane.messagesPosted()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	leave()

	select {
	case outcome := <-outcomes:
		holds := plane.holds(taskRun.TaskRunID)
		if outcome.Decision != mcpserver.ApprovalDecisionHeld || len(holds) != 1 || holds[0].State != holdrecord.StatePending {
			t.Fatalf("a question whose turn was cut off came back %+v with %+v, expected the run parked and the hold pending", outcome, holds)
		}
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("posting the question never ended after the turn was cut off")
	}
}

func TestOneReplyAfterASessionReloadResumesTheRunOnceAndRepliesOnceWithOneReading(t *testing.T) {
	plane := aParkingPlane(t, approvalgate.RejectOptionKey)
	firstSession := openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	taskRun := plane.parkARun(t)
	if errorValue := loadSessionForTest(t, plane.connection, firstSession, sessionMeta("sample@example.test", parkedConversationID)); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}

	plane.replyInTheThread(t, firstSession, "message-2", "ㄴ")

	launches := plane.launches()
	if len(launches) != 1 || launches[0].ExistingTaskRunID != taskRun.TaskRunID || !launches[0].IsRuntimeRestartResume {
		t.Fatalf("the reply launched %+v, expected the parked run resumed once", launches)
	}
	if len(launches[0].SettledCalls) != 1 || launches[0].SettledCalls[0].ToolName != "event_delete" || !launches[0].SettledCalls[0].Result.Failed() {
		t.Fatalf("the resumed run was told %+v, expected its held call carried as declined", launches[0].SettledCalls)
	}
	if !slices.Contains(launches[0].ActiveGoal.OutcomeContract.RequiredEvidenceTools, "event_delete") {
		t.Fatalf("the resumed run does not owe the call it was held on: %+v", launches[0].ActiveGoal.OutcomeContract)
	}
	if plane.reader.reads() != 1 {
		t.Fatalf("the reply was read %d times, expected once", plane.reader.reads())
	}
	messages := plane.messagesPosted()
	if len(messages) != 2 || messages[1] != "처리했습니다" || plane.client.permissionRequests() != 0 {
		t.Fatalf("the session was sent %v and %d permission requests, expected the question and then one reply", messages, plane.client.permissionRequests())
	}
	if holds := plane.holds(taskRun.TaskRunID); len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
		t.Fatalf("holds %+v: expected the one hold declined", holds)
	}
}

func TestLoadingASessionPostsAQuestionThatWasNeverPostedOnceAndWaitsForNothing(t *testing.T) {
	plane := aParkingPlane(t, "")
	taskRun := runWaitingOnAHeldCall(t, plane.taskRunService, parkedConversationID, heldCallForTest())

	for range 2 {
		if errorValue := loadSessionForTest(t, plane.connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", parkedConversationID)); errorValue != nil {
			t.Fatalf("load session: %v", errorValue)
		}
		deadline := time.Now().Add(5 * time.Second)
		for connectors.PostedApprovalQuestionMessageID(plane.taskRunService.ListTaskEvent(taskRun.TaskRunID)) == "" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(200 * time.Millisecond)

	if messages := plane.messagesPosted(); len(messages) != 1 || messages[0] != heldCallForTest().Confirmation || plane.client.permissionRequests() != 0 {
		t.Fatalf("two reloads sent %v and %d permission requests, expected the unposted question once", messages, plane.client.permissionRequests())
	}
	if len(plane.launches()) != 0 {
		t.Fatalf("a reload launched %d turns, and nothing is waited for or resumed by a reload", len(plane.launches()))
	}
}

func TestAQuestionParkedInAnotherConversationIsNotPostedOnAReload(t *testing.T) {
	plane := aParkingPlane(t, "")
	runWaitingOnAHeldCall(t, plane.taskRunService, "another-conversation", heldCallForTest())

	if errorValue := loadSessionForTest(t, plane.connection, "session-the-relay-still-holds", sessionMeta("sample@example.test", parkedConversationID)); errorValue != nil {
		t.Fatalf("load session: %v", errorValue)
	}
	time.Sleep(200 * time.Millisecond)

	if messages := plane.messagesPosted(); len(messages) != 0 {
		t.Fatalf("a session was sent the question of a run in a conversation it does not own: %v", messages)
	}
}

func TestAnUnrelatedRequestInTheThreadClosesTheHoldAndRunsAsAnOrdinaryTurn(t *testing.T) {
	plane := aParkingPlane(t, "")
	plane.launcher.startsANewRun = true
	sessionID := openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	parked := plane.parkARun(t)

	plane.replyInTheThread(t, sessionID, "message-2", "오늘 서울 날씨 알려줘")

	launches := plane.launches()
	if len(launches) != 1 || launches[0].IsRuntimeRestartResume || launches[0].Prompt != "오늘 서울 날씨 알려줘" {
		t.Fatalf("the unrelated request launched %+v, expected one ordinary turn carrying its own words", launches)
	}
	if launches[0].ActiveGoal.TaskRunID != parked.TaskRunID || launches[0].ExistingTaskRunID != parked.TaskRunID {
		t.Fatalf("the turn was offered the goal %+v, expected the parked run so the intake can continue, revise or leave it", launches[0].ActiveGoal)
	}
	if len(launches[0].SettledCalls) != 1 || !strings.Contains(launches[0].SettledCalls[0].Result.Failure.UserSafeSummary, "without choosing") {
		t.Fatalf("the turn was told %+v, expected the held call carried as not run because the requester chose nothing", launches[0].SettledCalls)
	}
	if holds := plane.holds(parked.TaskRunID); len(holds) != 1 || holds[0].State == holdrecord.StatePending {
		t.Fatalf("holds %+v: expected the hold closed as moved on", holds)
	}
	if status := mustFindTaskRun(t, plane.taskRunService, parked.TaskRunID).Status; status != agentcontract.TaskStatusCancelled {
		t.Fatalf("the launcher started another run and the parked one is %s, expected it closed quietly", status)
	}
	if plane.reader.reads() != 1 {
		t.Fatalf("the message was read %d times, expected once", plane.reader.reads())
	}
}

func handoffHeldCall() agentcontract.HeldCall {
	heldCall := heldCallForTest()
	heldCall.HandoffID = "handoff-1"
	return heldCall
}

func (plane *parkingPlane) reportHandoffEnded(sessionID acp.SessionId, handoffID string, optionKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errorValue := plane.connection.CallExtension(ctx, HandoffEndedExtensionMethod, HandoffEndedReport{SessionID: string(sessionID), HandoffID: handoffID, OptionKey: optionKey})
	return errorValue
}

func TestAHandoffTheRelayReportsEndedSettlesTheParkedRunAndResumesItOnce(t *testing.T) {
	plane := aParkingPlane(t, "")
	plane.launcher.launchedSignal = make(chan agentruntime.TaskLaunchRequest, 4)
	sessionID := openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	taskRun := runWaitingOnAHeldCall(t, plane.taskRunService, parkedConversationID, handoffHeldCall())

	if errorValue := plane.reportHandoffEnded(sessionID, "handoff-1", approvalgate.ApproveOptionKey); errorValue != nil {
		t.Fatalf("the relay's report was refused: %v", errorValue)
	}

	select {
	case resumed := <-plane.launcher.launchedSignal:
		if resumed.ExistingTaskRunID != taskRun.TaskRunID || !resumed.IsRuntimeRestartResume {
			t.Fatalf("the hand-back resumed %+v, expected the parked run", resumed)
		}
	case <-agenttest.WhenTheTestRunsOut(t):
		t.Fatal("the handed-back run never resumed")
	}
	if holds := plane.holds(taskRun.TaskRunID); len(holds) != 1 || holds[0].State != holdrecord.StateApproved {
		t.Fatalf("holds %+v: expected the hand-over approved for the resumed call", holds)
	}
	if plane.reader.reads() != 0 {
		t.Fatalf("a hand-back names its option, and it was read %d times", plane.reader.reads())
	}
	if errorValue := plane.reportHandoffEnded(sessionID, "handoff-1", approvalgate.ApproveOptionKey); errorValue == nil {
		t.Fatal("a second report of the same hand-back was taken, and would resume the run twice")
	}
}

func TestAHandoffEndNamingNoParkedRunOrAnOptionNotOfferedIsRefused(t *testing.T) {
	plane := aParkingPlane(t, "")
	sessionID := openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	taskRun := runWaitingOnAHeldCall(t, plane.taskRunService, parkedConversationID, handoffHeldCall())

	unknownHandoff := plane.reportHandoffEnded(sessionID, "handoff-nobody-waits-on", approvalgate.ApproveOptionKey)
	unofferedOption := plane.reportHandoffEnded(sessionID, "handoff-1", "approve-forever")

	if unknownHandoff == nil || unofferedOption == nil {
		t.Fatalf("reports that settle nothing were accepted: %v %v", unknownHandoff, unofferedOption)
	}
	if holds := plane.holds(taskRun.TaskRunID); len(holds) != 1 || holds[0].State != holdrecord.StatePending || len(plane.launches()) != 0 {
		t.Fatalf("holds %+v after %d launches: expected the hand-over still waiting", holds, len(plane.launches()))
	}
}

func TestAReplyThatPicksNoOptionLeavesTheParkedRunToTheTurnThatContinuesIt(t *testing.T) {
	plane := aParkingPlane(t, "")
	sessionID := openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
	parked := plane.parkARun(t)

	plane.replyInTheThread(t, sessionID, "message-2", "아 내일 말고 모레 회의야")

	launches := plane.launches()
	if len(launches) != 1 || launches[0].ExistingTaskRunID != parked.TaskRunID {
		t.Fatalf("the reply launched %+v, expected one turn on the parked run", launches)
	}
	if status := mustFindTaskRun(t, plane.taskRunService, parked.TaskRunID).Status; status == agentcontract.TaskStatusCancelled {
		t.Fatal("the run the turn continued was closed as left behind")
	}
	if messages := plane.messagesPosted(); len(messages) != 2 {
		t.Fatalf("the session was sent %v, expected the question and one reply", messages)
	}
}

func (plane *parkingPlane) parkARunOnAHandoff(t *testing.T) agentcontract.TaskRun {
	t.Helper()
	taskRun := plane.taskRunService.CreateTaskRunWithOrigin("person-sample", task.TaskRunOrigin{ConversationID: parkedConversationID, ReplyTargetID: "thread-root", IsThread: true}, "로그인해줘")
	named := approvalgate.ApprovalTarget{InputField: "handoff", ID: "handoff-1", Title: "https://portal.example.com", HandoffID: "handoff-1"}
	outcome, errorValue := plane.gate.AwaitApproval(approvalgate.WithRefusalTarget(context.Background(), named), plane.heldCallRequest(taskRun.TaskRunID))
	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("the hand-over came back %+v %v, expected the run parked on it", outcome, errorValue)
	}
	return taskRun
}

func (plane *parkingPlane) handoffsClosedOnceSettled(t *testing.T, expectedCount int) []string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if closed := plane.client.handoffsClosed(); len(closed) >= expectedCount {
			return closed
		}
		select {
		case <-deadline:
			return plane.client.handoffsClosed()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestAHandoffTheRequesterDeclinesOrLeavesInWordsIsClosedAtTheRelay(t *testing.T) {
	for name, readOptionID := range map[string]string{"declined": approvalgate.RejectOptionKey, "moved on": ""} {
		t.Run(name, func(t *testing.T) {
			plane := aParkingPlane(t, readOptionID)
			plane.launcher.startsANewRun = true
			sessionID := openSessionForTest(t, plane.connection, sessionMeta("sample@example.test", parkedConversationID))
			taskRun := plane.parkARunOnAHandoff(t)

			plane.replyInTheThread(t, sessionID, "message-2", "안 할래, 오늘 날씨나 알려줘")

			if closed := plane.handoffsClosedOnceSettled(t, 1); len(closed) != 1 || closed[0] != "handoff-1" {
				t.Fatalf("the relay was told to close %v, so the hand-over page stays open until it expires", closed)
			}
			if holds := plane.holds(taskRun.TaskRunID); len(holds) != 1 || holds[0].State != holdrecord.StateRejected {
				t.Fatalf("holds %+v: expected the hand-over question closed", holds)
			}
		})
	}
}

func TestAHandoffHandedBackOrAnOrdinaryQuestionDeclinedClosesNothingAtTheRelay(t *testing.T) {
	handedBack := aParkingPlane(t, "")
	handedBackSession := openSessionForTest(t, handedBack.connection, sessionMeta("sample@example.test", parkedConversationID))
	handedBack.parkARunOnAHandoff(t)
	if errorValue := handedBack.reportHandoffEnded(handedBackSession, "handoff-1", approvalgate.ApproveOptionKey); errorValue != nil {
		t.Fatalf("the relay's report was refused: %v", errorValue)
	}

	declined := aParkingPlane(t, approvalgate.RejectOptionKey)
	declinedSession := openSessionForTest(t, declined.connection, sessionMeta("sample@example.test", parkedConversationID))
	declined.parkARun(t)
	declined.replyInTheThread(t, declinedSession, "message-2", "아니 지우지 마")

	time.Sleep(50 * time.Millisecond)
	if closed := append(handedBack.client.handoffsClosed(), declined.client.handoffsClosed()...); len(closed) != 0 {
		t.Fatalf("the relay was told to close %v, and none of these left a hand-over unfinished", closed)
	}
}
