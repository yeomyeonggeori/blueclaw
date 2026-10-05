package approvalgate

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type holdState string

const (
	holdPending  holdState = "pending"
	holdApproved holdState = "approved"
	holdRejected holdState = "rejected"
	holdDeferred holdState = "deferred"
	holdSpent    holdState = "spent"

	decisionConfirm = "confirm"
	decisionCancel  = "cancel"
)

var stateAfterDecision = map[string]holdState{
	decisionConfirm:  holdApproved,
	decisionCancel:   holdRejected,
	deferredDecision: holdDeferred,
}

type hold struct {
	ID    string
	Call  agentcontract.HeldCall
	State holdState
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
	HoldID   string `json:"holdID"`
	ToolName string `json:"toolName"`
}

func holdsOf(taskEvents []agentcontract.TaskEvent) []hold {
	holds := []hold{}
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalPendingCall:
			holds = append(holds, holdFromEvent(taskEvent))
		case agentcontract.TaskEventApprovalDecided:
			decided := decodeEventBody[decidedRecord](taskEvent.Body)
			updateHold(holds, decided.HoldID, func(held *hold) { held.decide(decided.Decision) })
		case agentcontract.TaskEventApprovalExecuted:
			updateHold(holds, decodeEventBody[spentRecord](taskEvent.Body).HoldID, func(held *hold) { held.spend() })
		}
	}
	return holds
}

func holdFromEvent(taskEvent agentcontract.TaskEvent) hold {
	record := decodeEventBody[heldCallRecord](taskEvent.Body)
	record.ToolName = strings.TrimSpace(record.ToolName)
	return hold{ID: firstNonEmpty(record.HoldID, taskEvent.TaskEventID), Call: record.HeldCall, State: holdPending}
}

func updateHold(holds []hold, holdID string, update func(*hold)) {
	for index := range holds {
		if holds[index].ID == holdID {
			update(&holds[index])
			return
		}
	}
}

func (held *hold) decide(decision string) {
	if state, isKnown := stateAfterDecision[decision]; isKnown && held.State == holdPending {
		held.State = state
	}
}

func (held *hold) spend() {
	if held.State == holdPending || held.State == holdApproved {
		held.State = holdSpent
	}
}

func latestHold(holds []hold, state holdState) (hold, bool) {
	for index := len(holds) - 1; index >= 0; index-- {
		if holds[index].State == state {
			return holds[index], true
		}
	}
	return hold{}, false
}

func approvedHoldForCall(holds []hold, toolName string, toolInput json.RawMessage) (hold, bool) {
	callKey := agentcontract.CanonicalToolCallKey(toolName, toolInput)
	for _, held := range holds {
		if held.State == holdApproved && held.Call.CanonicalCallKey() == callKey {
			return held, true
		}
	}
	return hold{}, false
}

func newHoldID() string {
	return taskstate.NewIdentifier()
}

func decodeEventBody[Body any](body string) Body {
	var decodedBody Body
	json.Unmarshal([]byte(body), &decodedBody)
	return decodedBody
}
