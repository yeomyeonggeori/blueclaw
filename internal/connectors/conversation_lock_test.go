package connectors

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type blockingIntakeDecider struct {
	mutex      sync.Mutex
	callCount  int
	entered    chan struct{}
	release    chan struct{}
	addressing agentcontract.AddressingDecision
	turnFields agentcontract.TurnDecision
}

func (decider *blockingIntakeDecider) Decide(_ context.Context, request agentcontract.IntakeDecisionRequest, _ *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	decider.mutex.Lock()
	decider.callCount++
	isFirstCall := decider.callCount == 1
	decider.mutex.Unlock()
	if isFirstCall {
		close(decider.entered)
		<-decider.release
	}
	decisions := agentcontract.IntakeDecisions{}
	for _, message := range request.Messages {
		decisions.Messages = append(decisions.Messages, agentcontract.IntakeMessageDecision{
			MessageID:  message.MessageID,
			Addressing: decider.addressing,
			TurnFields: decider.turnFields,
		})
	}
	return decisions, nil
}

func TestASlowIntakeCallDoesNotParkTheNextMessageOnTheConversationLock(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "ok"})
	decider := &blockingIntakeDecider{
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
		addressing: addressedToBot(),
		turnFields: startTaskTurnDecision(),
	}
	connectorRuntime.UseIntakeDecider(decider)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	releaseIntake := sync.OnceFunc(func() { close(decider.release) })
	t.Cleanup(releaseIntake)

	firstEvent := testChannelInboundEvent("first")
	secondEvent := testChannelInboundEvent("second")
	secondEvent.SenderID = "another-sender"
	secondEvent.ReplyTargetID = "second"

	firstFinished := make(chan struct{})
	go func() {
		connectorRuntime.processQueuedConnectorEvent(context.Background(), QueuedConnectorEvent{Event: firstEvent})
		close(firstFinished)
	}()
	select {
	case <-decider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("expected the first message to reach the intake model")
	}

	secondReturned := make(chan struct{})
	go func() {
		connectorRuntime.processQueuedConnectorEvent(context.Background(), QueuedConnectorEvent{Event: secondEvent})
		close(secondReturned)
	}()
	select {
	case <-secondReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("the second message parked its inbox worker on the conversation lock while the first message's intake call was still in flight")
	}

	repository.mutex.Lock()
	releasedEvents := append([]QueuedConnectorEvent{}, repository.releasedEvents...)
	repository.mutex.Unlock()
	if len(releasedEvents) != 1 || releasedEvents[0].Event.MessageID != "second" {
		t.Fatalf("expected the second message handed back to the queue instead of held, got %+v", releasedEvents)
	}

	releaseIntake()
	select {
	case <-firstFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("expected the first message to finish once the intake model answered")
	}

	claimedEvents, errorValue := repository.ClaimPendingConnectorEvents(1, connectorClaimLeaseDuration)
	if errorValue != nil || len(claimedEvents) != 1 || claimedEvents[0].Event.MessageID != "second" {
		t.Fatalf("expected the released message to be claimable again, events=%+v error=%v", claimedEvents, errorValue)
	}
	connectorRuntime.processQueuedConnectorEvent(context.Background(), claimedEvents[0])
	repository.mutex.Lock()
	succeededCount := len(repository.succeededEvents)
	repository.mutex.Unlock()
	if succeededCount != 2 {
		t.Fatalf("expected both messages to reach an outcome, got %d", succeededCount)
	}
}
