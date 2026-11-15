//go:build !nobundledharness

package approvalgate

import (
	"context"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type knownSchedules struct {
	*recordingScheduler
	schedules map[string]task.Schedule
}

func (schedules knownSchedules) ScheduleByID(scheduleID string) (task.Schedule, bool, error) {
	schedule, isFound := schedules.schedules[scheduleID]
	return schedule, isFound, nil
}

func scheduleOf(creatorPersonID string, isApproved bool) task.Schedule {
	schedule := task.Schedule{ScheduleID: "schedule-weekly", CreatorPersonID: creatorPersonID}
	if isApproved {
		approvedAt := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
		schedule.ApprovedAt = &approvedAt
	}
	return schedule
}

func scheduledRunGate(t *testing.T, schedule task.Schedule) (*Gate, *task.TaskRunService, *recordingPoster, mcpserver.ApprovalRequest) {
	t.Helper()
	gate, taskRunService, taskRun, personAsker := gateWithPoster(t)
	gate.UseSchedules(knownSchedules{recordingScheduler: &recordingScheduler{}, schedules: map[string]task.Schedule{schedule.ScheduleID: schedule}})
	approvalRequest := approvalRequestFixture(taskRun.TaskRunID)
	approvalRequest.ConversationID = task.ScheduleSessionID(schedule.ScheduleID)
	return gate, taskRunService, personAsker, approvalRequest
}

func ledgerOf(taskRunService *task.TaskRunService, taskRunID string) string {
	ledger := []string{}
	for _, event := range taskRunService.ListTaskEvent(taskRunID) {
		ledger = append(ledger, event.Name+"="+event.Body)
	}
	return strings.Join(ledger, "\n")
}

func TestAnApprovedScheduleAnswersTheQuestionsItsRunsRaise(t *testing.T) {
	gate, taskRunService, personAsker, approvalRequest := scheduledRunGate(t, scheduleOf("person-1", true))

	outcome, errorValue := gate.AwaitApproval(context.Background(), approvalRequest)

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if outcome.Decision != mcpserver.ApprovalDecisionApproved || outcome.HoldID == "" {
		t.Fatalf("a run of an approved schedule came back %+v, expected an approved hold", outcome)
	}
	if len(personAsker.questions) != 0 {
		t.Fatalf("the schedule's run asked a person %+v", personAsker.questions)
	}
	ledger := ledgerOf(taskRunService, approvalRequest.TaskRunID)
	if !strings.Contains(ledger, `"decision":"approve","source":"schedule"`) || strings.Contains(ledger, agentcontract.TaskEventConfirmationRequested) {
		t.Fatalf("the ledger does not say the schedule decided without a question:\n%s", ledger)
	}
}

func TestARunOfAScheduleItsRequesterNeverApprovedStillAsksThem(t *testing.T) {
	for name, schedule := range map[string]task.Schedule{
		"never approved":              scheduleOf("person-1", false),
		"approved by someone else":    scheduleOf("person-2", true),
		"no longer kept by the agent": {},
	} {
		t.Run(name, func(t *testing.T) {
			gate, _, personAsker, approvalRequest := scheduledRunGate(t, schedule)
			approvalRequest.ConversationID = task.ScheduleSessionID("schedule-weekly")

			outcome, _ := gate.AwaitApproval(context.Background(), approvalRequest)

			if len(personAsker.questions) != 1 || outcome.Decision != mcpserver.ApprovalDecisionHeld {
				t.Fatalf("the run came back %+v without the requester being asked", outcome)
			}
		})
	}
}

func TestAnApprovedScheduleAllowsTheHarnessOnceForItsRun(t *testing.T) {
	gate, _, _, approvalRequest := scheduledRunGate(t, scheduleOf("person-1", true))
	options := []acp.PermissionOption{
		{OptionId: "allow-always", Kind: acp.PermissionOptionKindAllowAlways},
		{OptionId: "allow-once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
	}

	outcome, isAnswered := gate.AskHarnessPermission(context.Background(), approvalRequest, HarnessPermissionQuestion{Text: "run the report script", Options: options})

	if !isAnswered || outcome.Selected == nil || outcome.Selected.OptionId != "allow-once" {
		t.Fatalf("the harness was answered %+v (%v), expected allow-once", outcome, isAnswered)
	}
}
