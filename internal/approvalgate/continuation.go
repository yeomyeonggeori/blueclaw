package approvalgate

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func RecordRequesterDecision(taskRunStore taskstate.TaskRunStore, taskRunID string, approvalSignal *agentcontract.ApprovalSignal, source string) {
	if approvalSignal == nil || strings.TrimSpace(taskRunID) == "" {
		return
	}
	decision := decisionForApprovalSignal(*approvalSignal)
	if decision == "" {
		return
	}
	recordHoldDecision(taskRunStore, taskRunID, decision, source)
}

func recordHoldDecision(taskRunStore taskstate.TaskRunStore, taskRunID string, decision string, source string) {
	pendingHold, isPending := latestHold(holdsOf(taskRunStore.ListTaskEvent(taskRunID)), holdPending)
	if !isPending {
		return
	}
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalDecided, marshalEventBody(decidedRecord{HoldID: pendingHold.ID, Decision: decision, Source: source}))
	if decision == decisionConfirm {
		grantHoldScope(taskRunStore, taskRunID, pendingHold)
	}
}

func grantHoldScope(taskRunStore taskstate.TaskRunStore, taskRunID string, approvedHold hold) {
	approvalScope := strings.TrimSpace(approvedHold.Call.ApprovalScope)
	if approvalScope == "" {
		return
	}
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalScopeGranted, marshalEventBody(map[string]string{"scope": approvalScope}))
}

func decisionForApprovalSignal(approvalSignal agentcontract.ApprovalSignal) string {
	switch approvalSignal {
	case agentcontract.ApprovalSignalApprove:
		return decisionConfirm
	case agentcontract.ApprovalSignalReject:
		return decisionCancel
	}
	return ""
}

type ApprovedCall struct {
	HoldID    string
	ToolName  string
	ToolInput json.RawMessage
}

func ApprovedPendingCall(taskEvents []agentcontract.TaskEvent) (ApprovedCall, bool) {
	approvedHold, isApproved := latestHold(holdsOf(taskEvents), holdApproved)
	if !isApproved {
		return ApprovedCall{}, false
	}
	return ApprovedCall{HoldID: approvedHold.ID, ToolName: approvedHold.Call.ToolName, ToolInput: approvedHold.Call.ApprovedInput()}, true
}

func PendingHeldCall(taskEvents []agentcontract.TaskEvent) (agentcontract.HeldCall, bool) {
	pendingHold, isPending := latestHold(holdsOf(taskEvents), holdPending)
	return pendingHold.Call, isPending
}

func DeclinedCallNote(taskEvents []agentcontract.TaskEvent) string {
	holds := holdsOf(taskEvents)
	if len(holds) == 0 || holds[len(holds)-1].State != holdRejected {
		return ""
	}
	return "The requester declined the " + holds[len(holds)-1].Call.ToolName + " call you asked about. Do not attempt it again; continue without it or stop and say why you cannot."
}
