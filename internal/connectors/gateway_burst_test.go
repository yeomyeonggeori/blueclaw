package connectors

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func burstQueuedEvent(messageID string, conversationID string, senderID string, receivedAt time.Time) QueuedConnectorEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = conversationID
	event.SenderID = senderID
	event.RawReceivedAt = receivedAt
	event.Context.ConversationType = "channel"
	return QueuedConnectorEvent{Event: event}
}

func TestADirectMessageBurstWithNothingOpenMakesNoGatewayCall(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	recorder := &scriptedGatewayDecider{addressing: addressedToBot()}
	connectorRuntime.UseGatewayDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(time.Second)),
	}
	for index := range queuedEvents {
		queuedEvents[index].Event.Context.ConversationType = "dm"
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if recorder.calls() != 0 {
		t.Fatalf("expected a direct message with nothing open to make no gateway call, got %d", recorder.calls())
	}
}

func TestABurstFromOneSenderIsDecidedInOneCall(t *testing.T) {
	connectorRuntime, _, adapter := recordingGatewayDecisionRuntime(t)
	recorder := &scriptedGatewayDecider{addressing: addressedToBot()}
	connectorRuntime.UseGatewayDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(2*time.Second)),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt.Add(4*time.Second)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if recorder.calls() != 1 {
		t.Fatalf("expected one decision call for the burst, got %d", recorder.calls())
	}
	if len(recorder.requests[0].Messages) != 3 {
		t.Fatalf("expected the burst's three messages in one request, got %+v", recorder.requests[0].Messages)
	}
	for _, queuedEvent := range queuedEvents {
		decision, errorValue := connectorRuntime.judgeInboundMessage(context.Background(), adapter, queuedEvent.Event)
		if errorValue != nil {
			t.Fatalf("expected %s to read the burst decision: %v", queuedEvent.Event.MessageID, errorValue)
		}
		if decision.MessageID != queuedEvent.Event.MessageID {
			t.Fatalf("expected each message to read its own answer, got %+v", decision)
		}
	}
	if recorder.calls() != 1 {
		t.Fatalf("expected the burst decision to answer for every message without a second call, got %d", recorder.calls())
	}
}

func TestSendersAndConversationsAreDecidedApart(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	recorder := &scriptedGatewayDecider{addressing: addressedToBot()}
	connectorRuntime.UseGatewayDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "other-user", receivedAt),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-4", "channel-1", "sender-user", receivedAt),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if recorder.calls() != 1 {
		t.Fatalf("expected only the one sender's pair to be batched, got %d calls", recorder.calls())
	}
	if len(recorder.requests[0].Messages) != 2 {
		t.Fatalf("expected one sender's two messages, got %+v", recorder.requests[0].Messages)
	}
}

func TestAMessageOutsideTheBurstWindowIsDecidedOnItsOwn(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	recorder := &scriptedGatewayDecider{addressing: addressedToBot()}
	connectorRuntime.UseGatewayDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(time.Second)),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt.Add(time.Hour)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if recorder.calls() != 1 {
		t.Fatalf("expected the backlogged message to be left alone, got %d calls", recorder.calls())
	}
	if len(recorder.requests[0].Messages) != 2 {
		t.Fatalf("expected the two messages that arrived together, got %+v", recorder.requests[0].Messages)
	}
}

const testBurstPromptByteBudget = 80000

type budgetedGatewayDecider struct {
	scriptedGatewayDecider
}

func (decider *budgetedGatewayDecider) FitsBurstBudget(facts inboundengagement.Facts) bool {
	return promptByteCount(facts) <= testBurstPromptByteBudget
}

func promptByteCount(facts inboundengagement.Facts) int {
	byteCount := 0
	for _, message := range facts.Messages {
		byteCount += len(message.Prompt)
	}
	return byteCount
}

func TestABurstIsSplitSoEveryDecisionRequestFitsTheCeiling(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	recorder := &budgetedGatewayDecider{scriptedGatewayDecider: scriptedGatewayDecider{addressing: addressedToBot()}}
	connectorRuntime.UseGatewayDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	longPrompt := strings.Repeat("a", testBurstPromptByteBudget/2-1)
	queuedEvents := []QueuedConnectorEvent{}
	for index := 1; index <= 4; index++ {
		queuedEvent := burstQueuedEvent("message-"+strconv.Itoa(index), "direct-1", "sender-user", receivedAt.Add(time.Duration(index)*time.Second))
		queuedEvent.Event.Prompt = longPrompt
		queuedEvents = append(queuedEvents, queuedEvent)
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if recorder.calls() != 2 {
		t.Fatalf("expected the oversized burst to be split in two, got %d calls", recorder.calls())
	}
	decidedMessageIDs := []string{}
	for _, request := range recorder.requests {
		if byteCount := promptByteCount(request); byteCount > testBurstPromptByteBudget {
			t.Fatalf("expected every decision request to fit %d bytes, got %d", testBurstPromptByteBudget, byteCount)
		}
		for _, message := range request.Messages {
			decidedMessageIDs = append(decidedMessageIDs, message.MessageID)
		}
	}
	if len(decidedMessageIDs) != 4 {
		t.Fatalf("expected every message to be decided by one of the bursts, got %v", decidedMessageIDs)
	}
}

func TestAnAttachmentsOnlyGroupMessageNobodyAskedAboutIsNeverDecided(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	decisionModel := &refusingDecisionModel{}
	connectorRuntime.UseGatewayDecider(inboundengagement.NewDecisionModelDecider(decisionModel, func() float64 { return 1 }))
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-1", receivedAt, true, "이거 정리해줘"),
		burstChannelQueuedEvent("message-2", receivedAt.Add(time.Second), false, ""),
		burstChannelQueuedEvent("message-3", receivedAt.Add(2*time.Second), true, "이어서 부탁해"),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(decisionModel.decidedMessageCounts) != 1 {
		t.Fatalf("expected one decision call for the two mentions, got %v", decisionModel.decidedMessageCounts)
	}
	if decisionModel.decidedMessageCounts[0] != 2 {
		t.Fatalf("expected the uninvited attachment to be left out of the decision, got %d messages", decisionModel.decidedMessageCounts[0])
	}
}

func burstChannelQueuedEvent(messageID string, receivedAt time.Time, isBotMentioned bool, prompt string) QueuedConnectorEvent {
	event := testChannelInboundEvent(messageID)
	event.RawReceivedAt = receivedAt
	event.Prompt = prompt
	event.Context.Addressing = AddressingMetadata{BotMentioned: isBotMentioned}
	if prompt != "" {
		return QueuedConnectorEvent{Event: event}
	}
	event.Context.AttachmentsOnly = true
	event.InputParts = []agentcontract.AgentPart{{
		Type:  agentcontract.AgentPartTypeImage,
		Image: &agentcontract.AgentImagePart{MimeType: "image/png", Filename: "board.png", DataBase64: "aGVsbG8="},
	}}
	return QueuedConnectorEvent{Event: event}
}

type refusingDecisionModel struct {
	decidedMessageCounts []int
}

func (decisionModel *refusingDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.decidedMessageCounts = append(decisionModel.decidedMessageCounts, decidedMessageCount(request))
	return model.DecisionResponse{}, errors.New("the scripted decision model answers nothing")
}

func decidedMessageCount(request model.DecisionRequest) int {
	messageKeys := map[string]bool{}
	for questionKey := range request.Questions {
		messageKey, _, isKeyed := strings.Cut(questionKey, ".")
		if isKeyed {
			messageKeys[messageKey] = true
		}
	}
	return len(messageKeys)
}

func TestAnAttachmentsOnlyGroupMessageIsProcessedWithoutBeingDecided(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	decisionModel := &refusingDecisionModel{}
	connectorRuntime.UseGatewayDecider(inboundengagement.NewDecisionModelDecider(decisionModel, func() float64 { return 1 }))
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	repository.pendingEvents = []QueuedConnectorEvent{burstChannelQueuedEvent("message-1", time.Unix(1756800000, 0), false, "")}

	connectorRuntime.processNextQueuedConnectorEvent(context.Background())

	if len(decisionModel.decidedMessageCounts) != 0 {
		t.Fatalf("expected the ignored message to reach no decision call, got %v", decisionModel.decidedMessageCounts)
	}
	if len(repository.succeededEvents) != 1 || !repository.succeededEvents[0].Ignored {
		t.Fatalf("expected the message to be processed and ignored, got %+v", repository.succeededEvents)
	}
}

func TestAClaimedBurstIsDecidedOnceAndProcessedInArrivalOrder(t *testing.T) {
	connectorRuntime, _, adapter := recordingGatewayDecisionRuntime(t)
	recorder := &scriptedGatewayDecider{addressing: agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetHuman, ReactionEmoji: "eyes"}}
	connectorRuntime.UseGatewayDecider(recorder)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	receivedAt := time.Unix(1756800000, 0)
	repository.pendingEvents = []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-1", receivedAt, true, "이거 정리해줘"),
		burstChannelQueuedEvent("message-2", receivedAt.Add(time.Second), true, "이어서 부탁해"),
	}

	connectorRuntime.processNextQueuedConnectorEvent(context.Background())

	if recorder.calls() != 1 {
		t.Fatalf("expected the claimed burst to be decided in one call, got %d", recorder.calls())
	}
	decidedMessageIDs := []string{}
	for _, message := range recorder.requests[0].Messages {
		decidedMessageIDs = append(decidedMessageIDs, message.MessageID)
	}
	if len(decidedMessageIDs) != 2 || decidedMessageIDs[0] != "message-1" || decidedMessageIDs[1] != "message-2" {
		t.Fatalf("expected both messages in arrival order in the one decision, got %v", decidedMessageIDs)
	}
	reactedMessageIDs := []string{}
	for _, reaction := range adapter.reactions {
		if reaction.Reason == "addressing_ack" {
			reactedMessageIDs = append(reactedMessageIDs, reaction.MessageID)
		}
	}
	if len(reactedMessageIDs) != 2 || reactedMessageIDs[0] != "message-1" || reactedMessageIDs[1] != "message-2" {
		t.Fatalf("expected the burst to be processed in arrival order, got %v", reactedMessageIDs)
	}
}

func TestABurstRecordsItsOneDecisionCallOnceAgainstEveryMessageItJudged(t *testing.T) {
	connectorRuntime := NewConnectorRuntime(testConnectorIdentityService(), nil, task.NewTaskRunService(task.NewTaskEventService()), task.NewTaskEventService(), nil)
	connectorRuntime.RegisterAdapter(&testAdapter{senderEmail: "invited@example.com"})
	connectorRuntime.UseGatewayDecider(&recordingCallLedgerDecider{scriptedGatewayDecider: scriptedGatewayDecider{addressing: addressedToBot()}})
	recordedSubjects := [][]string{}
	connectorRuntime.UseTasklessLLMCallRecorder(func(subjects []string, _ agentcontract.LLMCallRecord) {
		recordedSubjects = append(recordedSubjects, subjects)
	})
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-1", receivedAt, true, "이거 정리해줘"),
		burstChannelQueuedEvent("message-2", receivedAt.Add(time.Second), true, "이어서 부탁해"),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recordedSubjects) != 1 || !slices.Equal(recordedSubjects[0], []string{"message-1", "message-2"}) {
		t.Fatalf("expected the burst's one call recorded once against both messages it judged, got %v", recordedSubjects)
	}
}

func TestACallOnARunningTasksMessageIsRecordedAgainstTheMessageAndNotOnTheTask(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	connectorRuntime.UseGatewayDecider(&recordingCallLedgerDecider{scriptedGatewayDecider: scriptedGatewayDecider{addressing: addressedToBot(), busyRoute: agentcontract.BusyRouteStatus}})
	recordedSubjects := [][]string{}
	connectorRuntime.UseTasklessLLMCallRecorder(func(subjects []string, _ agentcontract.LLMCallRecord) {
		recordedSubjects = append(recordedSubjects, subjects)
	})
	activeTaskRun := seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"}, "보고서 작성")
	eventsBefore := len(connectorRuntime.taskRunService.ListTaskEvent(activeTaskRun.TaskRunID))
	event := testInboundEvent("message-status")
	event.Prompt = "하고 있어?"

	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event); errorValue != nil {
		t.Fatalf("expected the busy message to process: %v", errorValue)
	}

	if len(recordedSubjects) != 1 || !slices.Equal(recordedSubjects[0], []string{"message-status"}) {
		t.Fatalf("expected the call recorded against its message, got %v", recordedSubjects)
	}
	for _, taskEvent := range connectorRuntime.taskRunService.ListTaskEvent(activeTaskRun.TaskRunID)[eventsBefore:] {
		if taskEvent.Name == "llm.call" {
			t.Fatalf("the gateway call landed on the running task, where the next judgment would read it as progress: %+v", taskEvent)
		}
	}
}

func TestAMessageForARunningTaskIsJudgedOnceThroughTheQueue(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	gatewayDecider := &scriptedGatewayDecider{addressing: addressedToBot(), busyRoute: agentcontract.BusyRouteStatus, relatesToActiveTask: true}
	connectorRuntime.UseGatewayDecider(gatewayDecider)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: "direct-1", ReplyTargetID: "reply-target-1"}, "보고서 작성")
	event := testInboundEvent("message-status")
	event.Prompt = "하고 있어?"
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event); errorValue != nil {
		t.Fatalf("expected the message to queue: %v", errorValue)
	}

	connectorRuntime.processNextQueuedConnectorEvent(context.Background())

	if len(repository.succeededEvents) != 1 || repository.succeededEvents[0].Reason != "busy_status" {
		t.Fatalf("expected the queued message to settle as a status question, got %+v", repository.succeededEvents)
	}
	if gatewayDecider.calls() != 1 {
		t.Fatalf("expected one gateway call for one message, got %d", gatewayDecider.calls())
	}
}

type recordingCallLedgerDecider struct{ scriptedGatewayDecider }

func (decider *recordingCallLedgerDecider) Decide(ctx context.Context, facts inboundengagement.Facts, observe agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	observe(agentcontract.LLMCallRecord{Kind: agentcontract.LLMCallKindDecision, Model: "decision-model", DecidedMessageIDs: decisionMessageIDs(facts.Messages)})
	return decider.scriptedGatewayDecider.Decide(ctx, facts, observe)
}

func decisionMessageIDs(messages []agentcontract.IntakeDecisionMessage) []string {
	messageIDs := []string{}
	for _, message := range messages {
		messageIDs = append(messageIDs, message.MessageID)
	}
	return messageIDs
}

func TestAThreadAnswerIsLeftOutOfTheBurst(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	recorder := &scriptedGatewayDecider{addressing: addressedToBot()}
	connectorRuntime.UseGatewayDecider(recorder)
	connectorRuntime.identityService.RememberPlatformAccount(identity.PlatformAccountIdentity{Platform: "test", ExternalUserID: "sender-user", Email: "invited@example.com"})
	connectorRuntime.askingThreads.join(&askingThread{taskRunID: "run-1", requesterPersonID: "person-1", platform: "test", conversationID: "direct-1", replyTargetID: "thread-root"})
	receivedAt := time.Unix(1756800000, 0)
	answer := burstQueuedEvent("message-answer", "direct-1", "sender-user", receivedAt)
	answer.Event.ReplyTargetID = "thread-root"
	isThread := true
	answer.Event.IsThread = &isThread
	queuedEvents := []QueuedConnectorEvent{
		answer,
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt.Add(time.Second)),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(2*time.Second)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if recorder.calls() != 1 {
		t.Fatalf("expected the two ordinary messages in one gateway call, got %d calls", recorder.calls())
	}
	if decidedMessageIDs := decisionMessageIDs(recorder.lastFacts().Messages); !slices.Equal(decidedMessageIDs, []string{"message-1", "message-2"}) {
		t.Fatalf("expected the thread answer to reach no gateway call, got %v", decidedMessageIDs)
	}
}

func TestAStopCommandIsLeftOutOfTheBurst(t *testing.T) {
	connectorRuntime, _, _ := recordingGatewayDecisionRuntime(t)
	recorder := &scriptedGatewayDecider{addressing: addressedToBot()}
	connectorRuntime.UseGatewayDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	stop := burstQueuedEvent("message-stop", "direct-1", "sender-user", receivedAt)
	stop.Event.Prompt = "/stop"
	queuedEvents := []QueuedConnectorEvent{
		stop,
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt.Add(time.Second)),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(2*time.Second)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if decidedMessageIDs := decisionMessageIDs(recorder.lastFacts().Messages); recorder.calls() != 1 || !slices.Equal(decidedMessageIDs, []string{"message-1", "message-2"}) {
		t.Fatalf("expected /stop to reach no gateway call, got %d calls over %v", recorder.calls(), decidedMessageIDs)
	}
}

func TestAnAttachmentsOnlyGroupMessageDuringARunningTaskMakesNoGatewayCall(t *testing.T) {
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	gatewayDecider := &scriptedGatewayDecider{addressing: addressedToBot(), busyRoute: agentcontract.BusyRouteSteer}
	connectorRuntime.UseGatewayDecider(gatewayDecider)
	event := burstChannelQueuedEvent("message-attachment", time.Unix(1756800000, 0), false, "").Event
	event.Prompt = "[image board.png]"
	isThread := true
	event.IsThread = &isThread
	seedRunningTaskRun(t, connectorRuntime.taskRunService, task.TaskRunOrigin{ConversationID: event.ConversationID, ReplyTargetID: event.ReplyTargetID}, "보고서 작성")

	result, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event)

	if errorValue != nil || !result.Ignored {
		t.Fatalf("expected the uninvited attachment to be ignored, got %+v %v", result, errorValue)
	}
	if gatewayDecider.calls() != 0 {
		t.Fatalf("expected no gateway call for an uninvited attachment, got %d", gatewayDecider.calls())
	}
}
