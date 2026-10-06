package approvalgate

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type ApprovedCall struct {
	HoldID    string
	ToolName  string
	ToolInput json.RawMessage
}

func ApprovedPendingCall(taskEvents []agentcontract.TaskEvent) (ApprovedCall, bool) {
	approvedHold, isApproved := holdrecord.LatestHold(holdrecord.Holds(taskEvents), holdrecord.StateApproved)
	if !isApproved {
		return ApprovedCall{}, false
	}
	return ApprovedCall{HoldID: approvedHold.ID, ToolName: approvedHold.Call.ToolName, ToolInput: approvedHold.Call.ApprovedInput()}, true
}

func PendingHeldCall(taskEvents []agentcontract.TaskEvent) (agentcontract.HeldCall, bool) {
	pendingHold, isPending := holdrecord.LatestHold(holdrecord.Holds(taskEvents), holdrecord.StatePending)
	return pendingHold.Call, isPending
}

func DeclinedCallNote(taskEvents []agentcontract.TaskEvent) string {
	holds := holdrecord.Holds(taskEvents)
	if len(holds) == 0 || holds[len(holds)-1].State != holdrecord.StateRejected {
		return ""
	}
	return "The requester declined the " + holds[len(holds)-1].Call.ToolName + " call you asked about. Do not attempt it again; continue without it or stop and say why you cannot."
}
