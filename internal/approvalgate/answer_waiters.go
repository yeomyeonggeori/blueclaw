package approvalgate

import "sync"

type answerWaiters struct {
	mutex      sync.Mutex
	taskRunIDs map[string]bool
}

func (gate *Gate) AwaitAnswer(taskRunID string) (leave func(), isTheWaiter bool) {
	gate.waiters.mutex.Lock()
	defer gate.waiters.mutex.Unlock()
	if gate.waiters.taskRunIDs[taskRunID] {
		return func() {}, false
	}
	if gate.waiters.taskRunIDs == nil {
		gate.waiters.taskRunIDs = map[string]bool{}
	}
	gate.waiters.taskRunIDs[taskRunID] = true
	return func() { gate.stopAwaiting(taskRunID) }, true
}

func (gate *Gate) IsAwaitingAnswer(taskRunID string) bool {
	gate.waiters.mutex.Lock()
	defer gate.waiters.mutex.Unlock()
	return gate.waiters.taskRunIDs[taskRunID]
}

func (gate *Gate) stopAwaiting(taskRunID string) {
	gate.waiters.mutex.Lock()
	defer gate.waiters.mutex.Unlock()
	delete(gate.waiters.taskRunIDs, taskRunID)
}
