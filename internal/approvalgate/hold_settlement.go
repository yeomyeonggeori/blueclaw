package approvalgate

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func SettleLatest(taskRunStore taskstate.TaskRunStore, taskRunID string, decision string, source string) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	pendingHold, isPending := holdrecord.LatestHold(holdrecord.Holds(taskRunStore.ListTaskEvent(taskRunID)), holdrecord.StatePending)
	if !isPending {
		return
	}
	holdrecord.Decide(taskRunStore, taskRunID, pendingHold.ID, decision, source)
}

func SettleSignal(taskRunStore taskstate.TaskRunStore, taskRunID string, approvalSignal *agentcontract.ApprovalSignal, source string) {
	if approvalSignal == nil {
		return
	}
	if !agentcontract.IsApprovalSignalName(string(*approvalSignal)) {
		return
	}
	SettleLatest(taskRunStore, taskRunID, string(*approvalSignal), source)
}
