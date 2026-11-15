//go:build !nobundledharness

package approvalgate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func recordedEventNames(taskRunService *task.TaskRunService, taskRunID string) []string {
	names := []string{}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		names = append(names, taskEvent.Name)
	}
	return names
}

func carriesEvent(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}

func taskRunStatus(t *testing.T, taskRunService *task.TaskRunService, taskRunID string) agentcontract.TaskStatus {
	t.Helper()
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		t.Fatalf("no task run %s", taskRunID)
	}
	return taskRun.Status
}

func TestAHeldCallParksTheRunWithItsQuestionPostedOnce(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if errorValue != nil || outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a call that needs the requester's answer is held, got %+v %v", outcome, errorValue)
	}
	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(holds) != 1 || holds[0].State != holdrecord.StatePending {
		t.Fatalf("the hold stays pending until an answer arrives as a message, got %+v", holds)
	}
	if len(poster.questions) != 1 || poster.questions[0].HoldID != holds[0].ID {
		t.Fatalf("the question of the hold is posted once, got %+v", poster.questions)
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) != agentcontract.TaskStatusWaitingApproval {
		t.Fatal("a held call left the run running, so nobody can see it is waiting")
	}
}

func TestTheRunIsPausedAndTheHoldRecordedBeforeTheQuestionIsPosted(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	statusWhilePosting, eventsWhilePosting := agentcontract.TaskStatus(""), []string{}
	poster.whilePosting = func() {
		statusWhilePosting = taskRunStatus(t, taskRunService, taskRun.TaskRunID)
		eventsWhilePosting = recordedEventNames(taskRunService, taskRun.TaskRunID)
	}

	if _, errorValue := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID)); errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if statusWhilePosting != agentcontract.TaskStatusWaitingApproval {
		t.Fatalf("the run read %q while the question was being posted, so an answer that arrives at once finds nothing waiting", statusWhilePosting)
	}
	if !carriesEvent(eventsWhilePosting, agentcontract.TaskEventApprovalHoldOpened) {
		t.Fatalf("the ledger carried %v while the question was being posted, so an answer cannot say which call was held", eventsWhilePosting)
	}
}

func TestAnApprovalRunsTheCallWhenTheResumedRunCallsItAgainAndSpendsItOnce(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	request := approvalRequestFixture(taskRun.TaskRunID)

	settlement, resumed := parkAndAnswer(t, gate, request, ApproveOptionKey)

	if settlement.Signal != agentcontract.ApprovalSignalApprove || settlement.SettledCall != nil {
		t.Fatalf("an approval settles nothing in the call's place, got %+v", settlement)
	}
	if resumed.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("the resumed run's call decided %q, expected approved", resumed.Decision)
	}
	names := recordedEventNames(taskRunService, taskRun.TaskRunID)
	for _, wanted := range []string{agentcontract.TaskEventApprovalHoldOpened, agentcontract.TaskEventApprovalDecided, agentcontract.TaskEventApprovalHoldSpent} {
		if !carriesEvent(names, wanted) {
			t.Fatalf("the ledger carries %v and not %s", names, wanted)
		}
	}
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || resumed.HoldID != holds[0].ID {
		t.Fatalf("the approved call is named by the hold that was answered, got %q for %+v", resumed.HoldID, holds)
	}
	if len(poster.questions) != 1 {
		t.Fatalf("the approved call was asked about %d times, expected once", len(poster.questions))
	}
	if again, _ := gate.AwaitApproval(context.Background(), request); again.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("one approval runs one call, so the same call again is asked about again, got %q", again.Decision)
	}
}

func TestADeclineIsTheHeldCallsResultAndNothingIsRecordedAsExecuted(t *testing.T) {
	gate, taskRunService, taskRun, _ := gateWithPoster(t)

	settlement, _ := parkAndAnswer(t, gate, approvalRequestFixture(taskRun.TaskRunID), RejectOptionKey)

	if settlement.Signal != agentcontract.ApprovalSignalReject || settlement.SettledCall == nil || settlement.SettledCall.ToolName != "event_delete" || !settlement.SettledCall.Result.Failed() {
		t.Fatalf("a decline is carried to the resumed run as the result of the held call, got %+v", settlement)
	}
	if carriesEvent(recordedEventNames(taskRunService, taskRun.TaskRunID), agentcontract.TaskEventApprovalHoldSpent) {
		t.Fatal("a declined call was recorded as executed")
	}
	if taskRunStatus(t, taskRunService, taskRun.TaskRunID) == agentcontract.TaskStatusWaitingApproval {
		t.Fatal("an answered run must not stay parked")
	}
}

func TestAHandoffTheRefusalNamesIsPostedAndKeptAsThatHandoff(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	named := ApprovalTarget{InputField: "handoff", ID: "handoff-1", Title: "https://portal.example.com", HandoffID: "handoff-1"}

	if _, errorValue := gate.AwaitApproval(WithRefusalTarget(context.Background(), named), approvalRequestFixture(taskRun.TaskRunID)); errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}

	if len(poster.questions) != 1 || poster.questions[0].HandoffID != "handoff-1" {
		t.Fatalf("the question's delivery must name the handoff, got %+v", poster.questions)
	}
	holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(holds) != 1 || holds[0].Call.HandoffID != "handoff-1" {
		t.Fatalf("the ledger must keep the handoff so its end can be matched to the parked run, got %+v", holds)
	}
}

func TestMovingOnClosesThePendingHoldAndTellsTheRunTheCallWasNotRun(t *testing.T) {
	gate, taskRunService, taskRun, _ := gateWithPoster(t)
	gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	movedOnCall, isMovedOn := MoveOnFromPendingHold(taskRunService, taskRun.TaskRunID)

	taskEvents := taskRunService.ListTaskEvent(taskRun.TaskRunID)
	holds := holdrecord.Holds(taskEvents)
	if !isMovedOn || len(holds) != 1 || holds[0].State == holdrecord.StatePending || movedOnCall.HoldID != holds[0].ID {
		t.Fatalf("moving on closes the hold it leaves, got %+v for %+v", movedOnCall, holds)
	}
	if !movedOnCall.Result.Failed() || !strings.Contains(movedOnCall.Result.Failure.UserSafeSummary, "without choosing") {
		t.Fatalf("the held call's result says the requester replied without choosing and it was not run, got %+v", movedOnCall.Result)
	}
	if note := DeclinedCallNote(taskEvents); note != "" {
		t.Fatalf("a requester who moved on did not decline the call, got the note %q", note)
	}
	if _, isPending := MoveOnFromPendingHold(taskRunService, taskRun.TaskRunID); isPending {
		t.Fatal("a hold is moved on from once")
	}
}

func TestEveryParkedQuestionIsOfferedAsOptionsByKey(t *testing.T) {
	plain := QuestionOffers(nil)
	if len(plain) != 2 || plain[0].ID != ApproveOptionKey || plain[1].ID != RejectOptionKey || !plain[1].IsDeclining {
		t.Fatalf("an approval is a question whose options are approve and reject, got %+v", plain)
	}
	deferring := QuestionOffers([]holdrecord.Choice{{Key: "offHours", StartsAt: laterStartsAt}, {Key: "now"}})
	if len(deferring) != 3 || deferring[0].ID != "offHours" || deferring[2].ID != CancelChoiceKey || !deferring[2].IsDeclining {
		t.Fatalf("a question with choices offers each choice and cancelling, got %+v", deferring)
	}
	answers := QuestionOffers([]holdrecord.Choice{{Key: "1", Label: "회의실 A"}})
	if answers[len(answers)-1].ID != approvalreply.OwnWordsOptionID {
		t.Fatalf("a question whose options are answers also takes the requester's own words, got %+v", answers)
	}
}

func TestAQuestionThatWillBePostedLaterStillParksTheRun(t *testing.T) {
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	poster.failure = errors.Join(ErrQuestionWillBePostedLater, errors.New("the messenger is down"))

	outcome, _ := gate.AwaitApproval(context.Background(), approvalRequestFixture(taskRun.TaskRunID))

	if outcome.Decision != mcpserver.ApprovalDecisionHeld {
		t.Fatalf("a question the messenger will carry once it is back still parks the run, got %q", outcome.Decision)
	}
	if holds := holdrecord.Holds(taskRunService.ListTaskEvent(taskRun.TaskRunID)); len(holds) != 1 || holds[0].State != holdrecord.StatePending {
		t.Fatalf("its hold stays pending for the one post it is owed, got %+v", holds)
	}
}
