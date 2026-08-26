//go:build appliance

// These scenarios drive an appliance workspace skill and assert on the real
// output of its bundled scripts, so they only build with the appliance tag and
// its skill bundle beside this checkout.
package e2e

import (
	"context"
	"github.com/yeomyeonggeori/blueclaw/internal/capability"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

func TestPresentationLocalMultiturnSuccessLive(t *testing.T) {
	if !truthyEnvironmentValue(os.Getenv("BLUECLAW_E2E_LIVE")) {
		t.Skip("set BLUECLAW_E2E_LIVE=1 to explicitly run costed live slides virtual session")
	}
	endpoint := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_ENDPOINT"))
	socketPath := strings.TrimSpace(os.Getenv("BLUECLAW_E2E_LLM_UNIX_SOCKET"))
	if endpoint == "" && socketPath == "" {
		t.Skip("set BLUECLAW_E2E_LLM_ENDPOINT or BLUECLAW_E2E_LLM_UNIX_SOCKET to run live slides virtual session")
	}
	scenario := PresentationLocalMultiturnSuccessScenario(t.TempDir())
	if skillDirectoryPath := rootPresentationSkillPath(); skillDirectoryPath != "" {
		scenario.Skills = nil
		scenario.SkillDirectoryPaths = []string{skillDirectoryPath}
	}
	scenario.LanguageModel = llm.CapabilityLLMClient{
		CapabilityClient: capability.NewClient(capability.Configuration{
			Endpoint:       endpoint,
			UnixSocketPath: socketPath,
		}),
		ModelName:     os.Getenv("BLUECLAW_E2E_LLM_MODEL"),
		ExecutionMode: firstNonEmptyTestString(os.Getenv("BLUECLAW_E2E_LLM_EXECUTION_MODE"), "auto"),
	}

	result, errorValue := RunVirtualSession(context.Background(), scenario)
	if errorValue != nil {
		t.Fatalf("expected slides scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 1 {
		t.Fatalf("expected one turn result, got %d", len(result.TurnResults))
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "tool.terminal_run.result", "exitCode") {
		t.Fatal("expected terminal build to succeed")
	}
}

func rootPresentationSkillPath() string {
	candidatePath := filepath.Clean("../../../../assets/blueclaw-workspace/skills/presentation")
	if _, errorValue := os.Stat(candidatePath); errorValue == nil {
		return candidatePath
	}
	return ""
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

func TestCompletionJudgeRecoveryAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), CompletionJudgeRecoveryAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected completion judge recovery scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected completed turn after judge recovery, got %s", turnResult.TaskStatus)
	}
	if countRequestedToolCalls(turnResult.Events, "task_add") != 1 {
		t.Fatalf("expected exactly one task_add, got events: %s", summarizeEvents(turnResult.Events))
	}
	if countRequestedToolCalls(turnResult.Events, "task_update") != 1 {
		t.Fatalf("expected a corrective task_update after the unsatisfied verdict, got events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "completion_judge.verdict") != 2 {
		t.Fatalf("expected two recorded completion judge verdicts, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "completion_judge.verdict", `"satisfied":false`) {
		t.Fatalf("expected an unsatisfied verdict recorded, got events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "completion_judge.verdict", `"satisfied":true`) {
		t.Fatalf("expected a satisfied verdict recorded, got events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "agent.evidence_missing") != 1 {
		t.Fatalf("expected the unsatisfied judge verdict to reject the first finish attempt, got events: %s", summarizeEvents(turnResult.Events))
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
	if !slices.Equal(scenario.Turns[0].ExpectedSelectedSkills, []string{"document"}) {
		t.Fatalf("expected document skill selection, got %v", scenario.Turns[0].ExpectedSelectedSkills)
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
	if eventsContain(turnResult.Events, "tool.terminal_run.requested", "") {
		t.Fatalf("ambient capture must not reach terminal_run; events: %s", summarizeEvents(turnResult.Events))
	}
	reviseResult := result.TurnResults[1]
	if !requestedToolCallPresent(reviseResult.Events, "task_update") {
		t.Fatalf("expected a same-thread follow-up to update the existing task; events: %s", summarizeEvents(reviseResult.Events))
	}
	if countRequestedToolCalls(reviseResult.Events, "task_add") > 0 {
		t.Fatalf("same-thread revision must update, not add a duplicate task; events: %s", summarizeEvents(reviseResult.Events))
	}
	if turnResult.DidReply || reviseResult.DidReply {
		t.Fatalf("ambient task capture must stay silent, got first=%q second=%q", turnResult.FinishMessage, reviseResult.FinishMessage)
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
	if !eventsContain(thirdTurnResult.Events, "tool.schedule_cancel.requested", "schedule_cancel") {
		t.Fatalf("expected deletion through the capability kernel; events: %s", summarizeEvents(thirdTurnResult.Events))
	}
	if activeScheduleCount(result.TaskSchedules) != 0 {
		t.Fatalf("expected zero active schedules, got %+v", result.TaskSchedules)
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
	if !eventsContain(approvalTurnResult.Events, "approval.executed", "event_delete") {
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
	if turnResult.DidReply {
		t.Fatalf("expected ambient calendar duty to stay silent, got %q", turnResult.FinishMessage)
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
	requestedBodies := eventBodies(turnResult.Events, "tool.skill_search.requested")
	if len(requestedBodies) != 1 {
		t.Fatalf("expected one skill_search request, got events: %s", summarizeEvents(turnResult.Events))
	}
	if strings.Contains(requestedBodies[0], "queries") || strings.Contains(requestedBodies[0], "limit") {
		t.Fatalf("expected empty skill_search input, got %s", requestedBodies[0])
	}
	if !eventsContain(turnResult.Events, "tool.skill_search.result", "presentation") {
		t.Fatalf("expected skill_search result to include presentation; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "presentation") {
		t.Fatalf("expected final reply to mention presentation, got %q", turnResult.FinishMessage)
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

func TestSitePrototypeAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SitePrototypeAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site prototype acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if !eventsContain(turnResult.Events, "agent.instructions_loaded", "website") {
		t.Fatal("expected site-prototype skill to be selected")
	}
	if !eventsContain(turnResult.Events, "tool.site_serve.result", "publishedURL") {
		t.Fatalf("expected site publish result to include a public URL; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.ModelContext, "site_serve") || !strings.Contains(turnResult.ModelContext, "site_serve") {
		t.Fatal("expected model context to document site app capabilities")
	}
}

func TestSiteEditRedeployAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteEditRedeployAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site edit redeploy acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 2 {
		t.Fatalf("expected two turn results, got %d", len(result.TurnResults))
	}
	secondTurnResult := result.TurnResults[1]
	if secondTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected second turn success, got %s", secondTurnResult.TaskStatus)
	}
	if countEvents(secondTurnResult.Events, "tool.terminal_run.requested") != 0 {
		t.Fatalf("expected no terminal_run for a content-only edit in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEventsWithFragment(secondTurnResult.Events, "tool.file_write.requested", "site-content.json") == 0 {
		t.Fatalf("expected a content-only site-content.json edit in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if countEventsWithFragment(secondTurnResult.Events, "tool.site_serve.requested", "site_serve") == 0 {
		t.Fatalf("expected site_serve capability invocation in turn two; events: %s", summarizeEvents(secondTurnResult.Events))
	}
	if !strings.Contains(secondTurnResult.FinishMessage, "https://") {
		t.Fatalf("expected final assistant message to contain a URL, got %q", secondTurnResult.FinishMessage)
	}
}

func TestSiteCustomStructureAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteCustomStructureAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site custom structure acceptance scenario to pass: %v", errorValue)
	}
	turnResult := result.TurnResults[0]
	if turnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected completed turn, got %s", turnResult.TaskStatus)
	}
	if !eventsContain(turnResult.Events, "tool.site_serve.result", "app/dist") {
		t.Fatalf("expected the first site_serve attempt to be rejected by the site owner for a missing build; events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "tool.terminal_run.requested") != 1 {
		t.Fatalf("expected exactly one terminal_run build after the app/src change; events: %s", summarizeEvents(turnResult.Events))
	}
	if countEvents(turnResult.Events, "tool.site_serve.requested") != 2 {
		t.Fatalf("expected site_serve to be attempted once before the build and once after; events: %s", summarizeEvents(turnResult.Events))
	}
	if !eventsContain(turnResult.Events, "tool.site_serve.result", "device.example.test") {
		t.Fatalf("expected the rebuilt site_serve to publish; events: %s", summarizeEvents(turnResult.Events))
	}
	if !strings.Contains(turnResult.FinishMessage, "https://") {
		t.Fatalf("expected final assistant message to contain a URL, got %q", turnResult.FinishMessage)
	}
}

func TestSiteLifecycleAcceptance(t *testing.T) {
	result, errorValue := RunVirtualSession(context.Background(), SiteLifecycleAcceptanceScenario(t.TempDir()))
	if errorValue != nil {
		t.Fatalf("expected site lifecycle acceptance scenario to pass: %v", errorValue)
	}
	if len(result.TurnResults) != 4 {
		t.Fatalf("expected four turn results, got %d", len(result.TurnResults))
	}
	deleteRequestTurnResult := result.TurnResults[2]
	if deleteRequestTurnResult.TaskStatus != task.TaskStatusWaitingApproval {
		t.Fatalf("expected delete turn to wait for approval, got %s", deleteRequestTurnResult.TaskStatus)
	}
	if !eventsContain(deleteRequestTurnResult.Events, "approval.pending_call", "site_unserve") {
		t.Fatalf("expected pending site_unserve approval; events: %s", summarizeEvents(deleteRequestTurnResult.Events))
	}
	deleteCompletionTurnResult := result.TurnResults[3]
	if deleteCompletionTurnResult.TaskStatus != task.TaskStatusCompleted {
		t.Fatalf("expected delete completion, got %s", deleteCompletionTurnResult.TaskStatus)
	}
	if !eventsContain(deleteCompletionTurnResult.Events, "tool.site_unserve.result", "deleted") {
		t.Fatalf("expected site_unserve result; events: %s", summarizeEvents(deleteCompletionTurnResult.Events))
	}
}
