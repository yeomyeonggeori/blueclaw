package approvalgate

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type questionThread struct {
	conversationID string
	replyTargetID  string
}

func (thread questionThread) isKnown() bool {
	return thread.conversationID != "" && thread.replyTargetID != ""
}

func threadOf(taskRun agentcontract.TaskRun) questionThread {
	return questionThread{
		conversationID: strings.TrimSpace(taskRun.OriginConversationID),
		replyTargetID:  strings.TrimSpace(taskRun.OriginReplyTargetID),
	}
}

func (gate *Gate) threadOfRun(taskRunID string) questionThread {
	taskRun, isFound := gate.taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return questionThread{}
	}
	return threadOf(taskRun)
}

func (gate *Gate) HasNewerQuestionInItsThread(taskRunID string) bool {
	taskRun, isFound := gate.taskRunService.FindTaskRun(taskRunID)
	if !isFound || !threadOf(taskRun).isKnown() {
		return false
	}
	askedAt, isAsking := gate.pendingQuestionOpenedAt(taskRunID)
	if !isAsking {
		return false
	}
	for _, other := range gate.taskRunService.ListTaskRunByPersonID(taskRun.RequesterPersonID) {
		if other.TaskRunID == taskRunID || !isWaitingForAnAnswer(other) || threadOf(other) != threadOf(taskRun) {
			continue
		}
		if otherAskedAt, isOtherAsking := gate.pendingQuestionOpenedAt(other.TaskRunID); isOtherAsking && otherAskedAt.After(askedAt) {
			return true
		}
	}
	return false
}

func isWaitingForAnAnswer(taskRun agentcontract.TaskRun) bool {
	return taskRun.Status == agentcontract.TaskStatusWaitingApproval || taskRun.Status == agentcontract.TaskStatusInterrupted
}

func (gate *Gate) pendingQuestionOpenedAt(taskRunID string) (time.Time, bool) {
	taskEvents := gate.taskRunService.ListTaskEvent(taskRunID)
	heldCall, isHeld := PendingHeldCall(taskEvents)
	if !isHeld {
		return time.Time{}, false
	}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != agentcontract.TaskEventApprovalHoldOpened {
			continue
		}
		var opened agentcontract.HeldCall
		if json.Unmarshal([]byte(taskEvent.Body), &opened) == nil && opened.HoldID == heldCall.HoldID {
			return taskEvent.CreatedAt, true
		}
	}
	return time.Time{}, false
}
