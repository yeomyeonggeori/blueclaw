package approvalgate

import (
	"encoding/json"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

type ApprovedCall struct {
	HoldID    string
	ToolName  string
	ToolInput json.RawMessage
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
