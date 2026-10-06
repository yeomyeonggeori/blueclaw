package connectors

import (
	"context"
	"sync"
	"sync/atomic"
)

type waitHandoff struct {
	unlockConversation func() (relock func())
	lendWorker         func() (reclaim func())
	pauseProgress      func() (resume func())
}

type waitHandoffKey struct{}

func withWaitHandoff(ctx context.Context, handoff waitHandoff) context.Context {
	return context.WithValue(ctx, waitHandoffKey{}, handoff)
}

func waitHandoffFrom(ctx context.Context) waitHandoff {
	handoff, _ := ctx.Value(waitHandoffKey{}).(waitHandoff)
	return handoff
}

func (handoff waitHandoff) holdingConversation(lock *sync.Mutex) waitHandoff {
	handoff.unlockConversation = func() func() {
		lock.Unlock()
		return lock.Lock
	}
	return handoff
}

func (handoff waitHandoff) pausingProgressOf(connectorRuntime *ConnectorRuntime, ctx context.Context, turn *inboundTurn) waitHandoff {
	handoff.pauseProgress = func() func() {
		if !turn.isProgressStarted {
			return func() {}
		}
		turn.endProgress()
		turn.isProgressStarted = false
		return func() { connectorRuntime.showTurnProgress(ctx, turn) }
	}
	return handoff
}

func (handoff waitHandoff) begin() func() {
	reverts := []func(){}
	if handoff.pauseProgress != nil {
		reverts = append(reverts, handoff.pauseProgress())
	}
	if handoff.unlockConversation != nil {
		reverts = append(reverts, handoff.unlockConversation())
	}
	if handoff.lendWorker != nil {
		reverts = append(reverts, handoff.lendWorker())
	}
	return func() {
		for index := len(reverts) - 1; index >= 0; index-- {
			reverts[index]()
		}
	}
}

type claimedEvents struct {
	mutex   sync.Mutex
	pending []QueuedConnectorEvent
}

func (claimed *claimedEvents) next() (QueuedConnectorEvent, bool) {
	claimed.mutex.Lock()
	defer claimed.mutex.Unlock()
	if len(claimed.pending) == 0 {
		return QueuedConnectorEvent{}, false
	}
	queuedEvent := claimed.pending[0]
	claimed.pending = claimed.pending[1:]
	return queuedEvent, true
}

func (connectorRuntime *ConnectorRuntime) processClaimedEvents(ctx context.Context, claimed *claimedEvents) {
	claimedContext := withWaitHandoff(ctx, waitHandoff{lendWorker: func() func() {
		return connectorRuntime.lendInboxWorker(ctx, claimed)
	}})
	for {
		queuedEvent, isLeft := claimed.next()
		if !isLeft {
			return
		}
		connectorRuntime.processQueuedConnectorEvent(claimedContext, queuedEvent)
	}
}

func (connectorRuntime *ConnectorRuntime) lendInboxWorker(ctx context.Context, claimed *claimedEvents) func() {
	isReclaimed := &atomic.Bool{}
	go func() {
		connectorRuntime.processClaimedEvents(ctx, claimed)
		for ctx.Err() == nil && !isReclaimed.Load() {
			if !connectorRuntime.processNextQueuedConnectorEvent(ctx) {
				sleepConnectorWorker(ctx)
			}
		}
	}()
	return func() { isReclaimed.Store(true) }
}
