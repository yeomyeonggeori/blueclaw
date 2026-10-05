package agentruntime

import (
	"sync"

	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/bluecollar/acpupdate"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type taskEventSubscription struct {
	taskRuns taskstate.TaskRunStore
	observer toolcallprogress.Observer
	mutex    sync.Mutex
	detach   func()
}

func subscribeToTaskRun(taskRuns taskstate.TaskRunStore, observer toolcallprogress.Observer, taskRunID string) *taskEventSubscription {
	subscription := &taskEventSubscription{taskRuns: taskRuns, observer: observer}
	subscription.follow(taskRunID)
	return subscription
}

func (subscription *taskEventSubscription) follow(taskRunID string) {
	if subscription.observer == nil {
		return
	}
	subscription.mutex.Lock()
	defer subscription.mutex.Unlock()
	subscription.stopLocked()
	subscription.detach = subscription.taskRuns.RegisterTaskRunObserver(taskRunID, subscription.observeEvent)
}

func (subscription *taskEventSubscription) observeEvent(rawTurnEvent taskstate.RawTurnEvent) {
	if update, isToolCall := acpupdate.ToolCallForEvent(rawTurnEvent); isToolCall {
		subscription.observer(update)
	}
}

func (subscription *taskEventSubscription) stop() {
	subscription.mutex.Lock()
	defer subscription.mutex.Unlock()
	subscription.stopLocked()
}

func (subscription *taskEventSubscription) stopLocked() {
	if subscription.detach == nil {
		return
	}
	subscription.detach()
	subscription.detach = nil
}
