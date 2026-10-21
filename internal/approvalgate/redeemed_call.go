package approvalgate

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const TaskEventApprovalHoldRedeemed = "approval.hold_redeemed"

type HeldCallRedemption struct {
	TaskRunID         string          `json:"taskRunID"`
	HoldID            string          `json:"holdID"`
	RequesterPersonID string          `json:"requesterPersonID"`
	ToolName          string          `json:"toolName"`
	ToolInput         json.RawMessage `json:"toolInput"`
}

var (
	ErrHoldBelongsToNoRun      = errors.New("no run holds this call")
	ErrHoldIsAnotherPersons    = errors.New("the run that holds this call was asked for by someone else")
	ErrHoldIsNotInThisRun      = errors.New("this run holds no call by that id")
	ErrHoldWasNotApproved      = errors.New("the person who asked never approved this call")
	ErrHoldWasRedeemedAlready  = errors.New("this approval was already carried out once")
	ErrHoldApprovedAnotherCall = errors.New("this approval was for a different call")
	redeemingOneHoldAtATime    sync.Mutex
)

func RedeemHeldCall(taskRunStore taskstate.TaskRunStore, redemption HeldCallRedemption) error {
	redeemingOneHoldAtATime.Lock()
	defer redeemingOneHoldAtATime.Unlock()
	taskRunID := strings.TrimSpace(redemption.TaskRunID)
	taskRun, isFound := taskRunStore.FindTaskRun(taskRunID)
	if !isFound || taskRunID == "" {
		return ErrHoldBelongsToNoRun
	}
	if taskRun.RequesterPersonID == "" || taskRun.RequesterPersonID != strings.TrimSpace(redemption.RequesterPersonID) {
		return ErrHoldIsAnotherPersons
	}
	taskEvents := taskRunStore.ListTaskEvent(taskRunID)
	if errorValue := redeemableHold(taskEvents, redemption); errorValue != nil {
		return errorValue
	}
	taskRunStore.AppendTaskEvent(taskRunID, TaskEventApprovalHoldRedeemed, marshalEventBody(map[string]any{
		"holdID":    redemption.HoldID,
		"toolName":  toolcontract.CanonicalToolName(redemption.ToolName),
		"toolInput": json.RawMessage(agentcontract.CanonicalToolInput(redemption.ToolInput)),
	}))
	return nil
}

func redeemableHold(taskEvents []agentcontract.TaskEvent, redemption HeldCallRedemption) error {
	held, isHeld := holdByID(holdrecord.Holds(taskEvents), redemption.HoldID)
	if !isHeld {
		return ErrHoldIsNotInThisRun
	}
	if !wasApproved(taskEvents, held.ID) {
		return ErrHoldWasNotApproved
	}
	if wasRedeemed(taskEvents, held.ID) {
		return ErrHoldWasRedeemedAlready
	}
	if !isTheApprovedCall(held, redemption) {
		return ErrHoldApprovedAnotherCall
	}
	return nil
}

func holdByID(holds []holdrecord.Hold, holdID string) (holdrecord.Hold, bool) {
	trimmedHoldID := strings.TrimSpace(holdID)
	for _, held := range holds {
		if trimmedHoldID != "" && held.ID == trimmedHoldID {
			return held, true
		}
	}
	return holdrecord.Hold{}, false
}

func wasApproved(taskEvents []agentcontract.TaskEvent, holdID string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != agentcontract.TaskEventApprovalDecided {
			continue
		}
		var decided struct {
			HoldID   string `json:"holdID"`
			Decision string `json:"decision"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &decided) == nil && decided.HoldID == holdID {
			return decided.Decision == holdrecord.DecisionApprove
		}
	}
	return false
}

func wasRedeemed(taskEvents []agentcontract.TaskEvent, holdID string) bool {
	for _, taskEvent := range taskEvents {
		var redeemed struct {
			HoldID string `json:"holdID"`
		}
		if taskEvent.Name == TaskEventApprovalHoldRedeemed && json.Unmarshal([]byte(taskEvent.Body), &redeemed) == nil && redeemed.HoldID == holdID {
			return true
		}
	}
	return false
}

func isTheApprovedCall(held holdrecord.Hold, redemption HeldCallRedemption) bool {
	if toolcontract.CanonicalToolName(held.Call.ToolName) != toolcontract.CanonicalToolName(redemption.ToolName) {
		return false
	}
	return agentcontract.CanonicalToolInput(held.Call.ApprovedInput()) == agentcontract.CanonicalToolInput(redemption.ToolInput)
}
