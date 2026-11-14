package connectors

import (
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
)

type heldMessageDecisions struct {
	mutex     sync.Mutex
	byMessage map[string]heldMessageDecision
}

type heldMessageDecision struct {
	taskRunID    string
	judgment     inboundengagement.Judgment
	isDecided    bool
	failureCount int
}

func newHeldMessageDecisions() *heldMessageDecisions {
	return &heldMessageDecisions{byMessage: map[string]heldMessageDecision{}}
}

func (held *heldMessageDecisions) about(event PlatformInboundEvent, taskRunID string) heldMessageDecision {
	held.mutex.Lock()
	defer held.mutex.Unlock()
	decision, isFound := held.byMessage[event.DedupeKey()]
	if !isFound || decision.taskRunID != taskRunID {
		return heldMessageDecision{taskRunID: taskRunID}
	}
	return decision
}

func (held *heldMessageDecisions) decidedJudgment(event PlatformInboundEvent) (inboundengagement.Judgment, bool) {
	held.mutex.Lock()
	defer held.mutex.Unlock()
	decision := held.byMessage[event.DedupeKey()]
	return decision.judgment, decision.isDecided
}

func (held *heldMessageDecisions) isHeld(event PlatformInboundEvent) bool {
	held.mutex.Lock()
	defer held.mutex.Unlock()
	_, isFound := held.byMessage[event.DedupeKey()]
	return isFound
}

func (held *heldMessageDecisions) keep(event PlatformInboundEvent, decision heldMessageDecision) {
	held.mutex.Lock()
	defer held.mutex.Unlock()
	held.byMessage[event.DedupeKey()] = decision
}

func (held *heldMessageDecisions) forget(event PlatformInboundEvent) {
	held.mutex.Lock()
	defer held.mutex.Unlock()
	delete(held.byMessage, event.DedupeKey())
}

func (held *heldMessageDecisions) nextAttemptAt(event PlatformInboundEvent) time.Time {
	held.mutex.Lock()
	decision := held.byMessage[event.DedupeKey()]
	held.mutex.Unlock()
	if decision.isDecided || decision.failureCount == 0 || decision.hasStoppedDeciding() {
		return time.Now().UTC().Add(connectorWorkerIdleDelay)
	}
	return nextConnectorAttemptAt(decision.failureCount)
}

func (decision heldMessageDecision) settledBy(judgment inboundengagement.Judgment, errorValue error) heldMessageDecision {
	if errorValue != nil {
		decision.failureCount++
		return decision
	}
	decision.judgment = judgment
	decision.isDecided = true
	return decision
}

func (decision heldMessageDecision) hasStoppedDeciding() bool {
	return !decision.isDecided && decision.failureCount >= connectorMaximumAttemptCount
}
