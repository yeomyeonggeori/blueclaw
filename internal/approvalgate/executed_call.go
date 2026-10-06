package approvalgate

import (
	"encoding/json"

	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func RecordApprovalSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, toolName string, toolInput json.RawMessage) string {
	approvedHold, _ := holdrecord.ApprovedHoldForCall(holdrecord.Holds(taskRunStore.ListTaskEvent(taskRunID)), toolName, toolInput)
	holdrecord.Spend(taskRunStore, taskRunID, approvedHold.ID, toolName, approvedInputOf(approvedHold, toolInput))
	return approvedHold.ID
}

func approvedInputOf(approvedHold holdrecord.Hold, requestedInput json.RawMessage) json.RawMessage {
	if approvedHold.ID == "" {
		return requestedInput
	}
	return approvedHold.Call.ApprovedInput()
}
