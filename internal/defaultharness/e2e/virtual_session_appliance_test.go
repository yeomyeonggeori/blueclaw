//go:build appliance && !nobundledharness

// These scenarios drive an appliance workspace skill and assert on the real
// output of its bundled scripts, so they only build with the appliance tag and
// its skill bundle beside this checkout.
package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestPresentationScenarioDoesNotScriptToolCalls(t *testing.T) {
	scenario := PresentationLocalMultiturnSuccessScenario(t.TempDir())
	if len(scenario.Turns) != 1 {
		t.Fatalf("expected one slides turn, got %d", len(scenario.Turns))
	}
	if len(scenario.Turns[0].ActionResponses) != 0 {
		t.Fatal("slides scenario must not script model tool calls or artifact creation")
	}
}

func officeSkillPath() string {
	return findScenarioSkillDirectory("office")
}

func TestToolPermissionScenarioReturnsPlannedFallback(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ToolPermissionHidesSkillScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected permission scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !strings.Contains(turnResult.FinishMessage, "필요한 도구") {
		t.Fatalf("expected planned fallback reply, got %q", turnResult.FinishMessage)
	}
}

func TestChangeCheckRecoveryAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ChangeCheckRecoveryAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected the change check recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected completed turn after recovering the unmet change, got %s", turnResult.TaskStatus)
	}
	if countRequestedToolCalls(turnResult.Events, "task_update") != 1 {
		t.Fatalf("expected a corrective task_update after the unmet change, got events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "completion.change_check") != 2 {
		t.Fatalf("expected two recorded change checks, got events: %s", summarizeEvents(turnResult.Events))
	}
}
func TestDocumentCreateAcceptanceUsesLiveCanonicalTools(t *testing.T) {
	scenario := DocumentCreateAcceptanceScenario(t.TempDir())
	if len(scenario.Turns) != 1 || len(scenario.Turns[0].ActionResponses) != 0 {
		t.Fatalf("expected one live-only document turn, got %+v", scenario.Turns)
	}
	if !slices.Equal(scenario.CapabilityToolNames, []string{"document_read"}) {
		t.Fatalf("expected canonical document capability, got %v", scenario.CapabilityToolNames)
	}
	if !slices.Equal(scenario.Turns[0].ExpectedSelectedSkills, []string{"office"}) {
		t.Fatalf("expected office skill selection, got %v", scenario.Turns[0].ExpectedSelectedSkills)
	}
	if len(scenario.Skills) != 1 || scenario.Skills[0].Name != "office" {
		t.Fatalf("the live run offers a skill directory only for the skills a scenario declares, so the document scenario must declare office, got %+v", scenario.Skills)
	}
	if scenario.Turns[0].ExpectedToolCallCounts["file_deliver"] != 1 {
		t.Fatalf("expected one final document delivery, got %+v", scenario.Turns[0].ExpectedToolCallCounts)
	}
}

func TestAmbientTaskCaptureAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AmbientTaskCaptureAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ambient task capture scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "agent.ambient_duty_launch", `"dutyName":"team_flow_update"`) {
		t.Fatalf("expected ambient duty launch for an other-person-mentioned task assignment; events: %s", summarizeEvents(turnResult.Events))
	}
	if eventsContain(turnResult.Events, "tool.bash.requested", "") {
		t.Fatalf("ambient capture must not reach shell; events: %s", summarizeEvents(turnResult.Events))
	}
	reviseResult := result.TurnResults[1]
	if !requestedToolCallPresent(reviseResult.Events, "task_update") {
		t.Fatalf("expected a same-thread follow-up to update the existing task; events: %s", summarizeEvents(reviseResult.Events))
	}
	if countRequestedToolCalls(reviseResult.Events, "task_add") > 0 {
		t.Fatalf("same-thread revision must update, not add a duplicate task; events: %s", summarizeEvents(reviseResult.Events))
	}
	if !turnResult.DidReply || !reviseResult.DidReply {
		t.Fatalf("ambient task capture must tell the room what it did, got first=%q second=%q", turnResult.FinishMessage, reviseResult.FinishMessage)
	}
}

func TestScheduleCreateAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ScheduleCreateAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected schedule acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.schedule_create.requested", "schedule_create") ||
		!eventsContain(turnResult.Events, "tool.schedule_create.result", "intervalSecond") {
		t.Fatalf("expected capability schedule create; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.ModelContext, "schedule_create") {
		t.Fatal("expected model context to document schedule_create capability")
	}
}

func TestScheduleLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), ScheduleLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected schedule lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 3 {
		t.Fatalf("expected three turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	thirdTurnResult := result.TurnResults[2]
	if !eventsContain(firstTurnResult.Events, "tool.schedule_create.requested", "schedule_create") ||
		!eventsContain(firstTurnResult.Events, "tool.schedule_create.result", "intervalSecond") {
		t.Fatalf("expected initial interval schedule through the capability kernel; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.schedule_update.requested", "schedule_update") ||
		!eventsContain(secondTurnResult.Events, "tool.schedule_update.result", "intervalSecond") {
		t.Fatalf("expected modification through the capability kernel; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(thirdTurnResult.Events, "tool.schedule_cancel.requested", "schedule_cancel") ||
		!eventsContain(thirdTurnResult.Events, "tool.schedule_cancel.result", "virtual-schedule-001") {
		t.Fatalf("expected deletion through the capability kernel; events: %s", summarizeEvents(thirdTurnResult.Events))
	}
}

func TestCalendarEventLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), CalendarEventLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected calendar event lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 4 {
		t.Fatalf("expected four turn results, got %d", len(result.TurnResults))
	}
	firstTurnResult := result.TurnResults[0]
	secondTurnResult := result.TurnResults[1]
	thirdTurnResult := result.TurnResults[2]
	approvalTurnResult := result.TurnResults[3]
	if countEventsWithFragment(firstTurnResult.Events, "tool.event_add.requested", "event_add") != 1 {
		t.Fatalf("expected one calendar add request; events: %s", summarizeEvents(firstTurnResult.Events))
	}
	if countEventsWithFragment(secondTurnResult.Events, "tool.event_update.requested", "event_update") != 1 {
		t.Fatalf("expected one calendar update request; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.event_update.requested", "2026-06-13T14:00:00+09:00") {
		t.Fatalf("expected updated time in calendar update input; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !eventsContain(secondTurnResult.Events, "tool.event_update.result", "updated virtual calendar event") {
		t.Fatalf("expected successful calendar update result; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEventsWithFragment(thirdTurnResult.Events, "tool.event_delete.requested", "event_delete") != 1 {
		t.Fatalf("expected one calendar delete request; events: %s", summarizeEvents(thirdTurnResult.Events))
	}
	if !eventsContain(approvalTurnResult.Events, "approval.hold_spent", "event_delete") {
		t.Fatalf("expected approved calendar delete execution; events: %s", summarizeEvents(approvalTurnResult.Events))
	}
}

func TestAmbientDutyCalendarAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), AmbientDutyCalendarAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected ambient duty calendar acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countRequestedToolCalls(turnResult.Events, "event_add") != 1 {
		t.Fatalf("expected one calendar add request; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "agent.ambient_duty_launch", `"dutyName":"calendar_upkeep"`) {
		t.Fatalf("expected ambient duty launch event; events: %s", summarizeEvents(turnResult.Events))
	}
	if !turnResult.DidReply {
		t.Fatal("an ambient calendar duty that added an event must say so")
	}
}

func TestCapabilityQuestionAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), CapabilityQuestionAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected capability question acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countEvents(turnResult.Events, "tool.skill_search.requested") != 0 {
		t.Fatalf("skill_search is an internal tool, so the model is never offered it; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "일정 예약") {
		t.Fatalf("expected final reply to name a capability from the loaded skills, got %q", turnResult.FinishMessage)
	}
}

func TestOneTimeScheduleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), OneTimeScheduleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected one-time schedule acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if countRequestedToolCalls(turnResult.Events, "schedule_create") != 1 {
		t.Fatalf("expected one-time schedule creation event; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.schedule_create.result", "schedule_create") {
		t.Fatalf("expected one-time schedule capability result; events: %s", summarizeEvents(turnResult.Events))
	}
}

func TestPresentationScenarioAsksForOutcomesNotForTheSkillsWording(t *testing.T) {
	turn := PresentationLocalMultiturnSuccessScenario(t.TempDir()).Turns[0]
	if !slices.Equal(turn.ExpectedAttachments, []string{".pptx"}) {
		t.Fatalf("the deck is a delivered, valid .pptx, got %v", turn.ExpectedAttachments)
	}
	if len(turn.ExpectedWorkspaceFiles) != 1 || !strings.Contains(turn.ExpectedWorkspaceFiles[0].PathGlob, "contact-sheet") {
		t.Fatalf("the deck must leave the render evidence the skill's build writes, got %+v", turn.ExpectedWorkspaceFiles)
	}
	for _, expectedCount := range turn.ExpectedEventCounts {
		if expectedCount.BodyFragment != `"output"` {
			t.Fatalf("the scenario pins a string the plugin may reword: %q", expectedCount.BodyFragment)
		}
	}
}
