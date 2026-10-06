package connectors

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type blockingGatewayDecider struct {
	scriptedGatewayDecider
	entered chan struct{}
	release chan struct{}

	hasBlockedACall atomic.Bool
}

func (decider *blockingGatewayDecider) Decide(ctx context.Context, facts inboundengagement.Facts, observe agentcontract.LLMCallObserver) ([]inboundengagement.Judgment, error) {
	if decider.hasBlockedACall.CompareAndSwap(false, true) {
		close(decider.entered)
		<-decider.release
	}
	return decider.scriptedGatewayDecider.Decide(ctx, facts, observe)
}

func TestASlowGatewayCallDoesNotParkTheNextMessageOnTheConversationLock(t *testing.T) {
	connectorRuntime, _ := newTestConnectorRuntime(t, testLanguageModel{reply: "ok"})
	decider := &blockingGatewayDecider{
		scriptedGatewayDecider: scriptedGatewayDecider{addressing: addressedToBot()},
		entered:                make(chan struct{}),
		release:                make(chan struct{}),
	}
	connectorRuntime.UseGatewayDecider(decider)
	repository := &testConnectorQueueRepository{}
	connectorRuntime.UseEventRepository(repository)
	releaseGateway := sync.OnceFunc(func() { close(decider.release) })
	t.Cleanup(releaseGateway)

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
		t.Fatal("expected the first message to reach the gateway model")
	}

	secondReturned := make(chan struct{})
	go func() {
		connectorRuntime.processQueuedConnectorEvent(context.Background(), QueuedConnectorEvent{Event: secondEvent})
		close(secondReturned)
	}()
	select {
	case <-secondReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("the second message parked its inbox worker on the conversation lock while the first message's gateway call was still in flight")
	}

	repository.mutex.Lock()
	releasedEvents := append([]QueuedConnectorEvent{}, repository.releasedEvents...)
	repository.mutex.Unlock()
	if len(releasedEvents) != 1 || releasedEvents[0].Event.MessageID != "second" {
		t.Fatalf("expected the second message handed back to the queue instead of held, got %+v", releasedEvents)
	}

	releaseGateway()
	select {
	case <-firstFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("expected the first message to finish once the gateway model answered")
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
