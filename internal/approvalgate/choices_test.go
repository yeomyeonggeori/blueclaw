//go:build !nobundledharness

package approvalgate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const laterStartsAt = "2099-10-03T03:00:00+09:00"

type recordingScheduler struct {
	requests []task.ApprovedCallScheduleRequest
}

func (scheduler *recordingScheduler) ScheduleApprovedCall(_ context.Context, request task.ApprovedCallScheduleRequest) (task.Schedule, error) {
	scheduler.requests = append(scheduler.requests, request)
	schedule, errorValue := task.BuildApprovedCallSchedule(request)
	schedule.ScheduleID = "schedule-1"
	return schedule, errorValue
}

func (scheduler *recordingScheduler) ScheduleByID(string) (task.Schedule, bool, error) {
	return task.Schedule{}, false, nil
}

func hostUpdateRequest(taskRunID string) mcpserver.ApprovalRequest {
	return mcpserver.ApprovalRequest{
		RequesterPersonID: "person-admin",
		TaskRunID:         taskRunID,
		ToolName:          "host_update",
		ToolInput:         json.RawMessage(`{}`),
		Prompt:            "update yourself",
		Platform:          "buzz",
		ConversationID:    "conversation-1",
		ReplyTargetID:     "message-1",
	}
}

func choiceResolver() *recordingTargetResolver {
	return &recordingTargetResolver{resolution: ApprovalTargetResolution{
		Target: ApprovalTarget{InputField: "targetVersion", ID: "v2026.10.02.090000", Title: "v2026.10.01.203142 → v2026.10.02.090000"},
		Choices: []holdrecord.Choice{
			{Key: "offHours", StartsAt: laterStartsAt},
			{Key: "now"},
		},
	}}
}

func choiceGate(t *testing.T) (*Gate, *task.TaskRunService, task.TaskRun, *recordingPoster, *recordingScheduler) {
	t.Helper()
	gate, taskRunService, taskRun, poster := gateWithPoster(t)
	gate.UseApprovalTargetResolver(choiceResolver())
	scheduler := &recordingScheduler{}
	gate.UseSchedules(scheduler)
	return gate, taskRunService, taskRun, poster, scheduler
}

func TestTheRequesterIsOfferedEveryChoiceTheTargetCarriesInItsOrder(t *testing.T) {
	gate, taskRunService, taskRun, poster, _ := choiceGate(t)

	if _, errorValue := gate.AwaitApproval(context.Background(), hostUpdateRequest(taskRun.TaskRunID)); errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if len(poster.questions) != 1 || len(poster.questions[0].Choices) != 2 || poster.questions[0].Choices[0].Key != "offHours" || poster.questions[0].Choices[1].Key != "now" {
		t.Fatalf("the requester was offered %+v, expected the later time first and now second", poster.questions)
	}
	recorded := OfferedChoices(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(recorded) != 2 || recorded[0].StartsAt != laterStartsAt {
		t.Fatalf("the ledger kept %+v, so a reply read after a restart is read against nothing", recorded)
	}
}

func TestChoosingNowRunsTheCallWhenTheResumedRunCallsAgain(t *testing.T) {
	gate, _, taskRun, _, scheduler := choiceGate(t)

	_, resumed := parkAndAnswer(t, gate, hostUpdateRequest(taskRun.TaskRunID), "now")
	if resumed.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("choosing now decided %q when the resumed run called again, expected approved", resumed.Decision)
	}
	if len(scheduler.requests) != 0 {
		t.Fatalf("choosing now also scheduled %d calls", len(scheduler.requests))
	}
}

func TestChoosingALaterTimeBindsTheApprovalToThatCallAndThatRequester(t *testing.T) {
	gate, taskRunService, taskRun, _, scheduler := choiceGate(t)

	settlement, _ := parkAndAnswer(t, gate, hostUpdateRequest(taskRun.TaskRunID), "offHours")
	if settlement.SettledCall == nil || settlement.SettledCall.Result.Failure != nil {
		t.Fatalf("choosing a later time settles the call as scheduled, got %+v", settlement)
	}
	if len(scheduler.requests) != 1 {
		t.Fatalf("choosing a later time scheduled %d calls, expected one", len(scheduler.requests))
	}
	scheduled := scheduler.requests[0]
	if scheduled.Call.ToolName != "host_update" || scheduled.ApproverPersonID != "person-admin" {
		t.Fatalf("the schedule carries %+v, expected host_update approved by person-admin", scheduled.Call)
	}
	if string(scheduled.Call.ToolInput) != `{"targetVersion":"v2026.10.02.090000"}` {
		t.Fatalf("the schedule carries input %s, expected the version the requester was shown", scheduled.Call.ToolInput)
	}
	if scheduled.StartsAt.Format(time.RFC3339) != laterStartsAt {
		t.Fatalf("the schedule starts at %s, expected %s", scheduled.StartsAt.Format(time.RFC3339), laterStartsAt)
	}
	if scheduled.Delivery.ConversationID != "conversation-1" {
		t.Fatalf("the schedule reports to %q, expected the conversation the approval was given in", scheduled.Delivery.ConversationID)
	}
	if _, isApproved := ApprovedPendingCall(taskRunService.ListTaskEvent(taskRun.TaskRunID)); isApproved {
		t.Fatal("the deferred call still reads as approved to run now, so a continuation would run it immediately")
	}
	if _, isHeld := PendingHeldCall(taskRunService.ListTaskEvent(taskRun.TaskRunID)); isHeld {
		t.Fatal("the deferred call still reads as waiting for an answer")
	}
}

func TestCancellingRunsNothingAndSchedulesNothing(t *testing.T) {
	gate, _, taskRun, _, scheduler := choiceGate(t)

	settlement, _ := parkAndAnswer(t, gate, hostUpdateRequest(taskRun.TaskRunID), CancelChoiceKey)
	if settlement.Signal != agentcontract.ApprovalSignalReject || settlement.SettledCall == nil || len(scheduler.requests) != 0 {
		t.Fatalf("cancelling settled %+v and scheduled %d calls", settlement, len(scheduler.requests))
	}
}

func TestTheDeferredCallReachesTheModelAsAScheduleWithNothingRunYet(t *testing.T) {
	gate, _, taskRun, _, _ := choiceGate(t)

	settlement, _ := parkAndAnswer(t, gate, hostUpdateRequest(taskRun.TaskRunID), "offHours")

	if settlement.SettledCall == nil || settlement.SettledCall.ToolName != "host_update" {
		t.Fatalf("the held call's result is what the resumed run is told, got %+v", settlement)
	}
	result := settlement.SettledCall.Result
	if result.Failure != nil || len(result.Effects) != 1 || result.Effects[0].ObjectType != "schedule" {
		t.Fatalf("the model was told %+v, expected one created schedule", result)
	}
}
