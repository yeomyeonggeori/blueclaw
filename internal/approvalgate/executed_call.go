package approvalgate

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
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

func RecordApprovedCallSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, approvedCall ApprovedCall) {
	holdrecord.Spend(taskRunStore, taskRunID, approvedCall.HoldID, approvedCall.ToolName, approvedCall.ToolInput)
}
