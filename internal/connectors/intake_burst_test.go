package connectors

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func burstQueuedEvent(messageID string, conversationID string, senderID string, receivedAt time.Time) QueuedConnectorEvent {
	event := testInboundEvent(messageID)
	event.ConversationID = conversationID
	event.SenderID = senderID
	event.RawReceivedAt = receivedAt
	return QueuedConnectorEvent{Event: event}
}

func TestABurstFromOneSenderIsDecidedInOneCall(t *testing.T) {
	connectorRuntime, _, adapter := recordingIntakeDecisionRuntime(t)
	recorder := &burstIntakeDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(2*time.Second)),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt.Add(4*time.Second)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recorder.requests) != 1 {
		t.Fatalf("expected one decision call for the burst, got %d", len(recorder.requests))
	}
	if len(recorder.requests[0].Messages) != 3 {
		t.Fatalf("expected the burst's three messages in one request, got %+v", recorder.requests[0].Messages)
	}
	for _, queuedEvent := range queuedEvents {
		decision, errorValue := connectorRuntime.decideInboundMessage(context.Background(), adapter, queuedEvent.Event)
		if errorValue != nil {
			t.Fatalf("expected %s to read the burst decision: %v", queuedEvent.Event.MessageID, errorValue)
		}
		if decision.MessageID != queuedEvent.Event.MessageID {
			t.Fatalf("expected each message to read its own answer, got %+v", decision)
		}
	}
	if len(recorder.requests) != 1 {
		t.Fatalf("expected the burst decision to answer for every message without a second call, got %d", len(recorder.requests))
	}
}

func TestSendersAndConversationsAreDecidedApart(t *testing.T) {
	connectorRuntime, _, _ := recordingIntakeDecisionRuntime(t)
	recorder := &burstIntakeDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "other-user", receivedAt),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-4", "channel-1", "sender-user", receivedAt),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recorder.requests) != 1 {
		t.Fatalf("expected only the one sender's pair to be batched, got %d calls", len(recorder.requests))
	}
	if len(recorder.requests[0].Messages) != 2 {
		t.Fatalf("expected one sender's two messages, got %+v", recorder.requests[0].Messages)
	}
}

func TestAMessageOutsideTheBurstWindowIsDecidedOnItsOwn(t *testing.T) {
	connectorRuntime, _, _ := recordingIntakeDecisionRuntime(t)
	recorder := &burstIntakeDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstQueuedEvent("message-1", "direct-1", "sender-user", receivedAt),
		burstQueuedEvent("message-2", "direct-1", "sender-user", receivedAt.Add(time.Second)),
		burstQueuedEvent("message-3", "direct-1", "sender-user", receivedAt.Add(time.Hour)),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recorder.requests) != 1 {
		t.Fatalf("expected the backlogged message to be left alone, got %d calls", len(recorder.requests))
	}
	if len(recorder.requests[0].Messages) != 2 {
		t.Fatalf("expected the two messages that arrived together, got %+v", recorder.requests[0].Messages)
	}
}

type burstIntakeDecider struct {
	requests []agentcontract.IntakeDecisionRequest
}

func (decider *burstIntakeDecider) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, _ *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	decider.requests = append(decider.requests, request)
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{
			MessageID:  message.MessageID,
			Addressing: addressedToBot(),
		})
	}
	return decisions, nil
}

func TestABurstIsSplitSoEveryDecisionRequestFitsTheCeiling(t *testing.T) {
	connectorRuntime, _, _ := recordingIntakeDecisionRuntime(t)
	recorder := &burstIntakeDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	receivedAt := time.Unix(1756800000, 0)
	longPrompt := strings.Repeat("a", connectorDecisionRequestByteCeiling/4)
	queuedEvents := []QueuedConnectorEvent{}
	for index := 1; index <= 4; index++ {
		queuedEvent := burstQueuedEvent("message-"+strconv.Itoa(index), "direct-1", "sender-user", receivedAt.Add(time.Duration(index)*time.Second))
		queuedEvent.Event.Prompt = longPrompt
		queuedEvents = append(queuedEvents, queuedEvent)
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)

	if len(recorder.requests) != 2 {
		t.Fatalf("expected the oversized burst to be split in two, got %d calls", len(recorder.requests))
	}
	decidedMessageIDs := []string{}
	for _, request := range recorder.requests {
		byteCount := intake.DecisionRequestByteCount(request)
		if byteCount > connectorDecisionRequestByteCeiling {
			t.Fatalf("expected every decision request to fit %d bytes, got %d", connectorDecisionRequestByteCeiling, byteCount)
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
	connectorRuntime, _, _ := recordingIntakeDecisionRuntime(t)
	describer := &countingAttachmentDescriber{}
	decisionModel := &refusingDecisionModel{}
	connectorRuntime.UseIntakeDecider(intake.NewDecisionPlanner(decisionModel, describer, func() float64 { return 1 }))
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
	if describer.callCount != 0 {
		t.Fatalf("expected no picture to be described for a message the gate ignores, got %d calls", describer.callCount)
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

type countingAttachmentDescriber struct {
	callCount int
}

func (describer *countingAttachmentDescriber) DescribeAttachments(context.Context, []agentcontract.AgentPart) ([]string, error) {
	describer.callCount++
	return []string{"화이트보드 사진."}, nil
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
	connectorRuntime, _, _ := recordingIntakeDecisionRuntime(t)
	describer := &countingAttachmentDescriber{}
	decisionModel := &refusingDecisionModel{}
	connectorRuntime.UseIntakeDecider(intake.NewDecisionPlanner(decisionModel, describer, func() float64 { return 1 }))
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	repository.pendingEvents = []QueuedConnectorEvent{burstChannelQueuedEvent("message-1", time.Unix(1756800000, 0), false, "")}

	connectorRuntime.processNextQueuedConnectorEvent(context.Background())

	if len(decisionModel.decidedMessageCounts) != 0 {
		t.Fatalf("expected the ignored message to reach no decision call, got %v", decisionModel.decidedMessageCounts)
	}
	if describer.callCount != 0 {
		t.Fatalf("expected no picture to be described for a message the gate ignores, got %d calls", describer.callCount)
	}
	if len(repository.succeededEvents) != 1 || !repository.succeededEvents[0].Ignored {
		t.Fatalf("expected the message to be processed and ignored, got %+v", repository.succeededEvents)
	}
}

func TestAClaimedBurstIsDecidedOnceAndProcessedInArrivalOrder(t *testing.T) {
	connectorRuntime, _, adapter := recordingIntakeDecisionRuntime(t)
	recorder := &reactingBurstDecider{}
	connectorRuntime.UseIntakeDecider(recorder)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	receivedAt := time.Unix(1756800000, 0)
	repository.pendingEvents = []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-1", receivedAt, true, "이거 정리해줘"),
		burstChannelQueuedEvent("message-2", receivedAt.Add(time.Second), true, "이어서 부탁해"),
	}

	connectorRuntime.processNextQueuedConnectorEvent(context.Background())

	if len(recorder.requests) != 1 {
		t.Fatalf("expected the claimed burst to be decided in one call, got %d", len(recorder.requests))
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

type reactingBurstDecider struct {
	requests []agentcontract.IntakeDecisionRequest
}

func (decider *reactingBurstDecider) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, _ *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	decider.requests = append(decider.requests, request)
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{
			MessageID:  message.MessageID,
			Addressing: agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetHuman, ReactionEmoji: "eyes"},
		})
	}
	return decisions, nil
}

func TestABurstRecordsItsOneDecisionCallInOneLedger(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	connectorRuntime := NewConnectorRuntime(testConnectorIdentityService(), nil, taskRunService, task.NewTaskEventService(), nil)
	connectorRuntime.UseTaskRunService(taskRunService)
	connectorRuntime.RegisterAdapter(&testAdapter{senderEmail: "invited@example.com"})
	connectorRuntime.UseIntakeDecider(&recordingCallLedgerDecider{})
	receivedAt := time.Unix(1756800000, 0)
	queuedEvents := []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-1", receivedAt, true, "이거 정리해줘"),
		burstChannelQueuedEvent("message-2", receivedAt.Add(time.Second), true, "이어서 부탁해"),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), queuedEvents)
	connectorRuntime.recordHeldIntakeCalls("task-1", queuedEvents[0].Event)
	connectorRuntime.recordHeldIntakeCalls("task-2", queuedEvents[1].Event)

	if len(taskRunService.ListTaskEvent("task-1")) != 1 {
		t.Fatalf("expected the first launched task to hold the burst's one decision call, got %+v", taskRunService.ListTaskEvent("task-1"))
	}
	if len(taskRunService.ListTaskEvent("task-2")) != 0 {
		t.Fatalf("expected the burst's decision call to be recorded once, got %+v", taskRunService.ListTaskEvent("task-2"))
	}
}

func TestADecisionNoTaskClaimsIsRecordedAgainstItsMessage(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	connectorRuntime := NewConnectorRuntime(testConnectorIdentityService(), nil, taskRunService, task.NewTaskEventService(), nil)
	connectorRuntime.UseTaskRunService(taskRunService)
	connectorRuntime.RegisterAdapter(&testAdapter{senderEmail: "invited@example.com"})
	connectorRuntime.UseIntakeDecider(&recordingCallLedgerDecider{})
	tasklessSubjects := [][]string{}
	connectorRuntime.UseTasklessLLMCallRecorder(func(subjects []string, _ agentcontract.LLMCallRecord) {
		tasklessSubjects = append(tasklessSubjects, subjects)
	})
	receivedAt := time.Unix(1756800000, 0)
	claimedEvents := []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-1", receivedAt, true, "이거 정리해줘"),
		burstChannelQueuedEvent("message-2", receivedAt.Add(time.Second), true, "이어서 부탁해"),
	}
	unclaimedEvents := []QueuedConnectorEvent{
		burstChannelQueuedEvent("message-3", receivedAt.Add(time.Minute), true, "다음 주 출시 확정됐어요!"),
		burstChannelQueuedEvent("message-4", receivedAt.Add(time.Minute+time.Second), true, "다들 고생했어요"),
	}

	connectorRuntime.decideClaimedBurst(context.Background(), claimedEvents)
	connectorRuntime.decideClaimedBurst(context.Background(), unclaimedEvents)
	connectorRuntime.recordHeldIntakeCalls("task-1", claimedEvents[0].Event)
	for _, queuedEvent := range append(claimedEvents, unclaimedEvents...) {
		connectorRuntime.recordUnclaimedIntakeCalls(queuedEvent.Event)
	}

	if len(tasklessSubjects) != 1 || !slices.Equal(tasklessSubjects[0], []string{"message-3", "message-4"}) {
		t.Fatalf("expected only the unclaimed decision recorded once against both messages it judged, got %v", tasklessSubjects)
	}
}

type recordingCallLedgerDecider struct{}

func (decider *recordingCallLedgerDecider) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, callLedger *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	if callLedger != nil {
		callLedger.Records = append(callLedger.Records, agentcontract.LLMCallRecord{Model: "decision-model", DecidedMessageIDs: decisionMessageIDs(request.Messages)})
	}
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{MessageID: message.MessageID, Addressing: addressedToBot()})
	}
	return decisions, nil
}

func decisionMessageIDs(messages []agentcontract.IntakeDecisionMessage) []string {
	messageIDs := []string{}
	for _, message := range messages {
		messageIDs = append(messageIDs, message.MessageID)
	}
	return messageIDs
}
