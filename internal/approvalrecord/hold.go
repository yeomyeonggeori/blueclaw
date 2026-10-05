package approvalrecord

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type State string

const (
	StatePending  State = "pending"
	StateApproved State = "approved"
	StateRejected State = "rejected"
	StateDeferred State = "deferred"
	StateSpent    State = "spent"

	DecisionConfirm = "confirm"
	DecisionCancel  = "cancel"
	DecisionDefer   = "defer"
)

var stateAfterDecision = map[string]State{
	DecisionConfirm: StateApproved,
	DecisionCancel:  StateRejected,
	DecisionDefer:   StateDeferred,
}

type Hold struct {
	ID    string
	Call  agentcontract.HeldCall
	State State
}

type heldCallRecord struct {
	agentcontract.HeldCall
	HoldID string `json:"holdID"`
}

type decidedRecord struct {
	HoldID   string `json:"holdID"`
	Decision string `json:"decision"`
	Source   string `json:"source"`
}

type spentRecord struct {
	HoldID string `json:"holdID"`
}

func Open(taskRunStore taskstate.TaskRunStore, taskRunID string, call agentcontract.HeldCall) string {
	holdID := taskstate.NewIdentifier()
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalPendingCall, marshal(heldCallRecord{HoldID: holdID, HeldCall: call}))
	return holdID
}

func Decide(taskRunStore taskstate.TaskRunStore, taskRunID string, holdID string, decision string, source string) {
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalDecided, marshal(decidedRecord{HoldID: holdID, Decision: decision, Source: source}))
}

func Spend(taskRunStore taskstate.TaskRunStore, taskRunID string, holdID string, body map[string]any) {
	body["holdID"] = holdID
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalExecuted, marshal(body))
}

func SpendApprovedCall(taskRunStore taskstate.TaskRunStore, taskRunID string, toolName string, toolInput json.RawMessage) (Hold, bool) {
	approved, isApproved := ApprovedHoldForCall(Holds(taskRunStore.ListTaskEvent(taskRunID)), toolName, toolInput)
	if !isApproved {
		return Hold{}, false
	}
	body := map[string]any{"toolName": strings.TrimSpace(toolName)}
	if len(toolInput) > 0 {
		body["toolInput"] = toolInput
	}
	Spend(taskRunStore, taskRunID, approved.ID, body)
	return approved, true
}

func Holds(taskEvents []agentcontract.TaskEvent) []Hold {
	holds := []Hold{}
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalPendingCall:
			holds = append(holds, holdFromEvent(taskEvent))
		case agentcontract.TaskEventApprovalDecided:
			decided := decode[decidedRecord](taskEvent.Body)
			update(holds, decided.HoldID, func(held *Hold) { held.decide(decided.Decision) })
		case agentcontract.TaskEventApprovalExecuted:
			update(holds, decode[spentRecord](taskEvent.Body).HoldID, func(held *Hold) { held.spend() })
		}
	}
	return holds
}

func holdFromEvent(taskEvent agentcontract.TaskEvent) Hold {
	record := decode[heldCallRecord](taskEvent.Body)
	record.ToolName = strings.TrimSpace(record.ToolName)
	return Hold{ID: firstNonEmpty(record.HoldID, taskEvent.TaskEventID), Call: record.HeldCall, State: StatePending}
}

func update(holds []Hold, holdID string, change func(*Hold)) {
	for index := range holds {
		if holds[index].ID == holdID {
			change(&holds[index])
			return
		}
	}
}

func (held *Hold) decide(decision string) {
	if state, isKnown := stateAfterDecision[decision]; isKnown && held.State == StatePending {
		held.State = state
	}
}

func (held *Hold) spend() {
	if held.State == StatePending || held.State == StateApproved {
		held.State = StateSpent
	}
}

func LatestHold(holds []Hold, state State) (Hold, bool) {
	for index := len(holds) - 1; index >= 0; index-- {
		if holds[index].State == state {
			return holds[index], true
		}
	}
	return Hold{}, false
}

func ApprovedHoldForCall(holds []Hold, toolName string, toolInput json.RawMessage) (Hold, bool) {
	callKey := agentcontract.CanonicalToolCallKey(toolName, toolInput)
	for _, held := range holds {
		if held.State == StateApproved && held.Call.CanonicalCallKey() == callKey {
			return held, true
		}
	}
	return Hold{}, false
}

func marshal(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func decode[Body any](body string) Body {
	var decoded Body
	json.Unmarshal([]byte(body), &decoded)
	return decoded
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
