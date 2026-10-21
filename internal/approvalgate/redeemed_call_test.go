package approvalgate

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

type heldCallFixture struct {
	taskRunService *task.TaskRunService
	taskRunID      string
	holdID         string
}

func aHeldMessage(t *testing.T, decision string) heldCallFixture {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "박예시에게 보내줘")
	holdID := holdrecord.Open(taskRunService, taskRun.TaskRunID, agentcontract.HeldCall{
		ToolName:          "message_send",
		ToolInput:         json.RawMessage(`{"recipientHint":"박예시","message":"내일 10시 어때요?"}`),
		ApprovedToolInput: json.RawMessage(`{"message":"내일 10시 어때요?","recipientHint":"person-2"}`),
	}, nil).ID
	if decision != "" {
		holdrecord.Decide(taskRunService, taskRun.TaskRunID, holdID, decision, "chat_reply")
	}
	return heldCallFixture{taskRunService: taskRunService, taskRunID: taskRun.TaskRunID, holdID: holdID}
}

func (fixture heldCallFixture) approvedCall() HeldCallRedemption {
	return HeldCallRedemption{
		TaskRunID:         fixture.taskRunID,
		HoldID:            fixture.holdID,
		RequesterPersonID: "person-1",
		ToolName:          "message_send",
		ToolInput:         json.RawMessage(`{ "recipientHint": "person-2", "message": "내일 10시 어때요?" }`),
	}
}

func TestAnApprovedHoldIsRedeemedByExactlyTheCallItApproved(t *testing.T) {
	fixture := aHeldMessage(t, holdrecord.DecisionApprove)

	if errorValue := RedeemHeldCall(fixture.taskRunService, fixture.approvedCall()); errorValue != nil {
		t.Fatalf("the approved call was refused: %v", errorValue)
	}
	if errorValue := RedeemHeldCall(fixture.taskRunService, fixture.approvedCall()); !errors.Is(errorValue, ErrHoldWasRedeemedAlready) {
		t.Fatalf("a second redemption of one approval answered %v", errorValue)
	}
}

func TestAHoldIsNotRedeemedForAnythingButTheCallItsRequesterApproved(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		decision string
		change   func(*HeldCallRedemption)
		expected error
	}{
		{"an invented hold id", holdrecord.DecisionApprove, func(call *HeldCallRedemption) { call.HoldID = "held-test" }, ErrHoldIsNotInThisRun},
		{"no hold id", holdrecord.DecisionApprove, func(call *HeldCallRedemption) { call.HoldID = "" }, ErrHoldIsNotInThisRun},
		{"a run that does not exist", holdrecord.DecisionApprove, func(call *HeldCallRedemption) { call.TaskRunID = "run-missing" }, ErrHoldBelongsToNoRun},
		{"another person's run", holdrecord.DecisionApprove, func(call *HeldCallRedemption) { call.RequesterPersonID = "person-2" }, ErrHoldIsAnotherPersons},
		{"a hold still waiting", "", func(*HeldCallRedemption) {}, ErrHoldWasNotApproved},
		{"a rejected hold", holdrecord.DecisionReject, func(*HeldCallRedemption) {}, ErrHoldWasNotApproved},
		{"a different message", holdrecord.DecisionApprove, func(call *HeldCallRedemption) {
			call.ToolInput = json.RawMessage(`{"recipientHint":"person-2","message":"회의 취소합니다"}`)
		}, ErrHoldApprovedAnotherCall},
		{"a different recipient", holdrecord.DecisionApprove, func(call *HeldCallRedemption) {
			call.ToolInput = json.RawMessage(`{"recipientHint":"person-3","message":"내일 10시 어때요?"}`)
		}, ErrHoldApprovedAnotherCall},
		{"a different tool", holdrecord.DecisionApprove, func(call *HeldCallRedemption) { call.ToolName = "mail_message_send" }, ErrHoldApprovedAnotherCall},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := aHeldMessage(t, testCase.decision)
			call := fixture.approvedCall()
			testCase.change(&call)

			if errorValue := RedeemHeldCall(fixture.taskRunService, call); !errors.Is(errorValue, testCase.expected) {
				t.Fatalf("expected %v, got %v", testCase.expected, errorValue)
			}
			if errorValue := RedeemHeldCall(fixture.taskRunService, fixture.approvedCall()); testCase.decision == holdrecord.DecisionApprove && errorValue != nil {
				t.Fatalf("a refused redemption spent the approval anyway: %v", errorValue)
			}
		})
	}
}

func TestOneApprovalCarriedOutByTwoCallsAtOnceRedeemsOnce(t *testing.T) {
	fixture := aHeldMessage(t, holdrecord.DecisionApprove)
	results := make(chan error, 8)
	var started sync.WaitGroup
	for range 8 {
		started.Add(1)
		go func() {
			defer started.Done()
			results <- RedeemHeldCall(fixture.taskRunService, fixture.approvedCall())
		}()
	}
	started.Wait()
	close(results)

	redeemed := 0
	for errorValue := range results {
		if errorValue == nil {
			redeemed++
		}
	}
	if redeemed != 1 {
		t.Fatalf("one approval was carried out %d times", redeemed)
	}
}
