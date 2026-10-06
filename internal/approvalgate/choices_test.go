package approvalgate

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const laterStartsAt = "2099-10-03T03:00:00+09:00"

type choosingAsker struct {
	answer          ApprovalAnswer
	offeredChoices  []holdrecord.Choice
	offeredQuestion string
}

func (asker *choosingAsker) AskPermission(_ context.Context, _ mcpserver.ApprovalRequest, question PermissionQuestion) (ApprovalAnswer, AskStatus) {
	asker.offeredChoices = question.Choices
	asker.offeredQuestion = question.Confirmation
	return asker.answer, AskAnswered
}

type recordingScheduler struct {
	requests []task.ApprovedCallScheduleRequest
}

func (scheduler *recordingScheduler) ScheduleApprovedCall(_ context.Context, request task.ApprovedCallScheduleRequest) (task.Schedule, error) {
	scheduler.requests = append(scheduler.requests, request)
	schedule, errorValue := task.BuildApprovedCallSchedule(request)
	schedule.ScheduleID = "schedule-1"
	return schedule, errorValue
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

func choiceGate(t *testing.T, answer ApprovalAnswer) (*Gate, *task.TaskRunService, task.TaskRun, *choosingAsker, *recordingScheduler) {
	t.Helper()
	gate, taskRunService, taskRun := gateFixture(t)
	gate.UseApprovalTargetResolver(choiceResolver())
	asker := &choosingAsker{answer: answer}
	gate.UsePermissionAsker(asker)
	scheduler := &recordingScheduler{}
	gate.UseApprovedCallScheduler(scheduler)
	return gate, taskRunService, taskRun, asker, scheduler
}

func TestTheRequesterIsOfferedEveryChoiceTheTargetCarriesInItsOrder(t *testing.T) {
	gate, taskRunService, taskRun, asker, _ := choiceGate(t, ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: "now"})

	if _, errorValue := gate.AwaitApproval(context.Background(), hostUpdateRequest(taskRun.TaskRunID)); errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if len(asker.offeredChoices) != 2 || asker.offeredChoices[0].Key != "offHours" || asker.offeredChoices[1].Key != "now" {
		t.Fatalf("the requester was offered %+v, expected the later time first and now second", asker.offeredChoices)
	}
	recorded := approvalrecord.OfferedChoices(taskRunService.ListTaskEvent(taskRun.TaskRunID))
	if len(recorded) != 2 || recorded[0].StartsAt != laterStartsAt {
		t.Fatalf("the ledger kept %+v, so a reply read after a restart is read against nothing", recorded)
	}
}

func TestChoosingNowRunsTheCallInsideTheTurn(t *testing.T) {
	gate, _, taskRun, _, scheduler := choiceGate(t, ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: "now"})

	outcome, errorValue := gate.AwaitApproval(context.Background(), hostUpdateRequest(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionApproved {
		t.Fatalf("choosing now decided %q, expected approved", outcome.Decision)
	}
	if len(scheduler.requests) != 0 {
		t.Fatalf("choosing now also scheduled %d calls", len(scheduler.requests))
	}
}

func TestChoosingALaterTimeBindsTheApprovalToThatCallAndThatRequester(t *testing.T) {
	gate, taskRunService, taskRun, _, scheduler := choiceGate(t, ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: "offHours"})

	outcome, errorValue := gate.AwaitApproval(context.Background(), hostUpdateRequest(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionDeferred {
		t.Fatalf("choosing a later time decided %q, expected deferred", outcome.Decision)
	}
	if len(scheduler.requests) != 1 {
		t.Fatalf("choosing a later time scheduled %d calls, expected one", len(scheduler.requests))
	}
	scheduled := scheduler.requests[0]
	if scheduled.Call.ToolName != "host_update" || scheduled.Call.ApproverPersonID != "person-admin" {
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
	gate, _, taskRun, _, scheduler := choiceGate(t, ApprovalAnswer{Signal: agentcontract.ApprovalSignalReject})

	outcome, errorValue := gate.AwaitApproval(context.Background(), hostUpdateRequest(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("await approval: %v", errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionRejected || len(scheduler.requests) != 0 {
		t.Fatalf("cancelling decided %q and scheduled %d calls", outcome.Decision, len(scheduler.requests))
	}
}

func TestTheDeferredCallReachesTheModelAsAScheduleWithNothingRunYet(t *testing.T) {
	gate, _, taskRun, _, _ := choiceGate(t, ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: "offHours"})
	review, errorValue := gate.TurnGate(TurnContext{RequesterPersonID: "person-admin", Platform: "buzz", ConversationID: "conversation-1", ReplyTargetID: "message-1"}).ReviewToolCall(
		toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID),
		toolcontract.ToolInvocation{ToolName: "host_update", Input: json.RawMessage(`{}`)},
		toolcontract.ToolDefinition{Name: "host_update", RequiresApproval: true},
	)
	if errorValue != nil {
		t.Fatalf("review: %v", errorValue)
	}
	if review.MayProceed {
		t.Fatal("a call deferred to a later time went ahead now")
	}
	if review.Result.Failure != nil || len(review.Result.Effects) != 1 || review.Result.Effects[0].ObjectType != "schedule" {
		t.Fatalf("the model was told %+v, expected one created schedule", review.Result)
	}
}
