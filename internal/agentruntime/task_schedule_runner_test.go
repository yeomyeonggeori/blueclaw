package agentruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func TestTaskScheduleRunnerLaunchesDueSchedule(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	harness := harnesstest.New(taskRunService)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory_search"},
	}, nil)
	provisioner := &recordingRequesterWorkspaceProvisioner{}
	taskLauncher := NewTaskLauncher(harness, taskRunService, toolCatalogBuilder)
	taskLauncher.UseRequesterWorkspaceProvisioner(provisioner)
	runAt := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)

	result, errorValue := NewTaskScheduleRunner(taskLauncher).RunIfDue(context.Background(), TaskScheduleRunRequest{
		TaskSchedule: task.TaskSchedule{
			TaskScheduleID:  "schedule-1",
			CreatorPersonID: "person-1",
			Prompt:          "daily brief",
			Kind:            task.TaskScheduleKindOnce,
			RunAt:           &runAt,
			NextRunAt:       &runAt,
		},
		ReferenceTime: runAt,
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatalf("expected schedule run to succeed: %v", errorValue)
	}
	if !result.DidRun {
		t.Fatal("expected due schedule to run")
	}
	if result.TaskSchedule.LastTaskRunID == "" {
		t.Fatalf("expected last task run id, got %+v", result.TaskSchedule)
	}
	if provisioner.callCount != 1 {
		t.Fatalf("expected scheduled launch to provision requester workspace, got %d calls", provisioner.callCount)
	}
	if result.TaskSchedule.LastRunAt == nil || !result.TaskSchedule.LastRunAt.Equal(runAt) {
		t.Fatalf("expected last run time, got %+v", result.TaskSchedule.LastRunAt)
	}
	if result.TaskSchedule.NextRunAt != nil {
		t.Fatalf("expected one-time schedule to complete, got next run %+v", result.TaskSchedule.NextRunAt)
	}

	taskEvents := taskEventService.ListTaskEvent(result.LaunchResult.TurnResult.TaskRun.TaskRunID)
	if !containsTaskEvent(taskEvents, "agent.task_launched") {
		t.Fatalf("expected scheduled launch event, got %+v", taskEvents)
	}
}

func TestTaskScheduleRunnerAddsCronContextToLaunch(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	languageModel := &capturingScheduleRuntimeLanguageModel{content: runtimeFinishMessage("scheduled done")}
	useScheduledRuntimeLanguageModel(agentKernel, languageModel)
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory_search"},
	}, nil)
	taskLauncher := routedTaskLauncher(agentKernel, taskRunService, toolCatalogBuilder, languageModel)
	nextRunAt := time.Date(2026, 6, 15, 23, 0, 0, 0, time.UTC)

	result, errorValue := NewTaskScheduleRunner(taskLauncher).RunIfDue(context.Background(), TaskScheduleRunRequest{
		TaskSchedule: task.TaskSchedule{
			TaskScheduleID:    "schedule-briefing",
			CreatorPersonID:   "person-1",
			Name:              "일일 브리핑",
			Prompt:            "오늘의 주요 일정, 날씨, 할 일을 브리핑한다.",
			Kind:              task.TaskScheduleKindCron,
			CronExpression:    "0 8 * * *",
			TimeZone:          "Asia/Seoul",
			NextRunAt:         &nextRunAt,
			CompletedRunCount: 3,
		},
		ReferenceTime: nextRunAt.Add(29 * time.Second),
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatalf("expected schedule run to succeed: %v", errorValue)
	}
	if !result.DidRun {
		t.Fatal("expected due schedule to run")
	}

	requestText := structuredRequestMessagesText(languageModel.requests)
	// How the prompt words a scheduled run belongs to the agent loop, which tests
	// its own wording. What this service owes is the context it hands over, so
	// that is what this reads.
	for _, expected := range []string{
		"Scheduled run:",
		`"scheduleID":"schedule-briefing"`,
		`"kind":"cron"`,
		`"cadence":"daily at 08:00 Asia/Seoul"`,
		`"cronExpression":"0 8 * * *"`,
		`"occurrenceAt":"2026-06-16T08:00:00+09:00"`,
		`"completedRunCount":3`,
	} {
		if !strings.Contains(requestText, expected) {
			t.Fatalf("expected launch request to include %q, got %s", expected, requestText)
		}
	}
	if !hasStructuredRequest(languageModel.requests, "bluecollar_turn_router") {
		t.Fatal("expected scheduled objective to use semantic routing")
	}

	taskLaunchEvent := findTaskEvent(taskEventService.ListTaskEvent(result.LaunchResult.TurnResult.TaskRun.TaskRunID), "agent.task_launched")
	if !strings.Contains(taskLaunchEvent.Body, `"scheduledRun"`) || !strings.Contains(taskLaunchEvent.Body, `"scheduleID":"schedule-briefing"`) {
		t.Fatalf("expected task launch event to include scheduled run context, got %s", taskLaunchEvent.Body)
	}
}

func TestTaskScheduleRunnerPreservesScheduledArtifactRouting(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	languageModel := &capturingScheduleRuntimeLanguageModel{
		content:       `{"action":"fail","reason":"artifact fixture stops after intake","goalStatus":"blocked","goalSatisfied":false}`,
		routerContent: `{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"high","requestedOutputFormats":["pptx"],"requestedOutputEvidence":"발표자료","expectedResults":[{"id":"presentation","type":"file","description":"PPTX 발표자료","required":true}],"requiredEvidence":["file_deliver"],"siteRequestEvidence":"","responseLanguage":"ko","reason":"scheduled presentation","userFacingReply":"","initialToolNames":["file_deliver"],"priorTaskReference":"none"}`,
	}
	useScheduledRuntimeLanguageModel(agentKernel, languageModel)
	taskLauncher := routedTaskLauncher(agentKernel, taskRunService, NewToolCatalogBuilder(), languageModel)
	runAt := time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)

	result, errorValue := NewTaskScheduleRunner(taskLauncher).RunIfDue(context.Background(), TaskScheduleRunRequest{
		TaskSchedule: task.TaskSchedule{
			TaskScheduleID:  "schedule-presentation",
			CreatorPersonID: "person-1",
			Prompt:          "이번 주 영업 현황을 발표자료로 정리해줘.",
			Kind:            task.TaskScheduleKindOnce,
			RunAt:           &runAt,
			NextRunAt:       &runAt,
		},
		ReferenceTime: runAt,
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	taskEvents := taskEventService.ListTaskEvent(result.LaunchResult.TurnResult.TaskRun.TaskRunID)
	intakeEvent := findTaskEvent(taskEvents, "agent.intake")
	if !strings.Contains(intakeEvent.Body, `"level":"xhigh"`) || !strings.Contains(intakeEvent.Body, `"requestedOutputFormats":["pptx"]`) {
		t.Fatalf("expected router artifact decision to retain formats and promote to xhigh, got %s", intakeEvent.Body)
	}
	goalEvent := findTaskEventByPrefix(taskEvents, "agent.goal.")
	if !strings.Contains(goalEvent.Body, `"requiredAttachmentSuffixes":[".pptx"]`) || !strings.Contains(goalEvent.Body, `"id":"presentation"`) {
		t.Fatalf("expected scheduled artifact outcome contract to survive routing, got %s", goalEvent.Body)
	}
}

type capturingScheduleRuntimeLanguageModel struct {
	content       string
	routerContent string
	requests      []llm.StructuredResponseRequest
}

func (languageModel *capturingScheduleRuntimeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *capturingScheduleRuntimeLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	if request.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: firstScheduleRuntimeRouterResponse(languageModel.routerContent)}, nil
	}
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

func scheduledRuntimeTurnRouterResponse() string {
	return `{"route":"start_task","classification":"bounded_task","taskShape":"research_task","level":"medium","requestedOutputFormats":null,"expectedResults":[],"requiredEvidence":[],"siteRequestEvidence":"","responseLanguage":"ko","reason":"scheduled objective","userFacingReply":"","initialToolNames":[],"priorTaskReference":"none"}`
}

func firstScheduleRuntimeRouterResponse(routerContent string) string {
	if strings.TrimSpace(routerContent) != "" {
		return routerContent
	}
	return scheduledRuntimeTurnRouterResponse()
}

func useScheduledRuntimeLanguageModel(agentKernel *loop.AgentKernel, languageModel llm.LanguageModelProvider) {
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
}

func hasStructuredRequest(requests []llm.StructuredResponseRequest, schemaName string) bool {
	for _, request := range requests {
		if request.StructuredOutputSchema.Name == schemaName {
			return true
		}
	}
	return false
}

func findTaskEventByPrefix(taskEvents []task.TaskEvent, namePrefix string) task.TaskEvent {
	for _, taskEvent := range taskEvents {
		if strings.HasPrefix(taskEvent.Name, namePrefix) {
			return taskEvent
		}
	}
	return task.TaskEvent{}
}

func structuredRequestMessagesText(requests []llm.StructuredResponseRequest) string {
	lines := []string{}
	for _, request := range requests {
		for _, message := range request.Messages {
			lines = append(lines, message.Content)
			for _, part := range message.Parts {
				lines = append(lines, part.Text)
			}
		}
	}
	return strings.Join(lines, "\n")
}
