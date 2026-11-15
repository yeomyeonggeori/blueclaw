package approvalgate

import (
	"context"
	"sync"
	"sync/atomic"
)

type answerWaiter struct {
	thread    questionThread
	supersede func()
}

type answerWaiters struct {
	mutex       sync.Mutex
	byTaskRunID map[string]*answerWaiter
}

func (gate *Gate) AwaitAnswer(taskRunID string, supersede func()) (leave func(), isTheWaiter bool) {
	waiter := &answerWaiter{thread: gate.threadOfRun(taskRunID), supersede: supersede}
	if !gate.waiters.enter(taskRunID, waiter) {
		return func() {}, false
	}
	return func() { gate.waiters.leave(taskRunID, waiter) }, true
}

func (gate *Gate) IsAwaitingAnswer(taskRunID string) bool {
	gate.waiters.mutex.Lock()
	defer gate.waiters.mutex.Unlock()
	_, isAwaited := gate.waiters.byTaskRunID[taskRunID]
	return isAwaited
}

func (gate *Gate) askAsTheThreadsQuestion(ctx context.Context, taskRunID string) (askContext context.Context, wasSuperseded func() bool, leave func()) {
	askContext, stopAsking := context.WithCancel(ctx)
	var isSuperseded atomic.Bool
	stopAwaiting, _ := gate.AwaitAnswer(taskRunID, func() {
		isSuperseded.Store(true)
		stopAsking()
	})
	for _, older := range gate.waiters.othersIn(taskRunID) {
		older.supersede()
	}
	return askContext, isSuperseded.Load, func() {
		stopAwaiting()
		stopAsking()
	}
}

func (waiters *answerWaiters) enter(taskRunID string, waiter *answerWaiter) bool {
	waiters.mutex.Lock()
	defer waiters.mutex.Unlock()
	if _, isTaken := waiters.byTaskRunID[taskRunID]; isTaken {
		return false
	}
	if waiters.byTaskRunID == nil {
		waiters.byTaskRunID = map[string]*answerWaiter{}
	}
	waiters.byTaskRunID[taskRunID] = waiter
	return true
}

func (waiters *answerWaiters) leave(taskRunID string, waiter *answerWaiter) {
	waiters.mutex.Lock()
	defer waiters.mutex.Unlock()
	if waiters.byTaskRunID[taskRunID] == waiter {
		delete(waiters.byTaskRunID, taskRunID)
	}
}

func (waiters *answerWaiters) othersIn(taskRunID string) []*answerWaiter {
	waiters.mutex.Lock()
	defer waiters.mutex.Unlock()
	asking, isWaiting := waiters.byTaskRunID[taskRunID]
	if !isWaiting || !asking.thread.isKnown() {
		return nil
	}
	others := []*answerWaiter{}
	for otherTaskRunID, waiter := range waiters.byTaskRunID {
		if otherTaskRunID != taskRunID && waiter.thread == asking.thread {
			delete(waiters.byTaskRunID, otherTaskRunID)
			others = append(others, waiter)
		}
	}
	return others
}
