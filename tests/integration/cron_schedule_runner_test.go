package integration

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/agentruntime"
	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/loop"
)

func TestCronScheduleRunsDailyResearchPromptAndAdvancesToNextDay(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	agentKernel := loop.NewAgentKernel(taskRunService, task.NewTaskStepService())
	languageModel := staticScheduleLanguageModel{content: scheduleFinishMessage("Today's research surfaced three key changes.")}
	useScheduleTestLanguageModel(agentKernel, languageModel)
	toolCatalogBuilder := agentruntime.NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(map[string][]string{
		"default": {"memory_search"},
	}, nil)
	runAt := time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC)
	nextRunAt := runAt

	taskLauncher := agentruntime.NewTaskLauncher(agentKernel, taskRunService, toolCatalogBuilder)
	taskLauncher.UseTurnRouter(intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(&intaketest.LanguageModelDecisionModel{LanguageModel: languageModel}, nil, nil), agentcontract.IntakeOptions{IsEnabled: true}))
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, languageModel))
	result, errorValue := agentruntime.NewScheduleRunner(taskLauncher).RunIfDue(context.Background(), agentruntime.ScheduleRunRequest{
		Schedule: task.Schedule{
			ScheduleID:       "schedule-daily-research",
			CreatorPersonID:  "person-1",
			Name:             "daily research brief",
			Prompt:           "research the industry news every day and give me the highlights at 9am.",
			AgentProfileName: "default",
			Kind:             task.ScheduleKindCron,
			CronExpression:   "0 9 * * *",
			TimeZone:         "Asia/Seoul",
			NextRunAt:        &nextRunAt,
		},
		ReferenceTime: runAt,
		PersonAccess:  policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100, GrantedClasses: []string{"internal"}},
		WorkspaceID:   "workspace-1",
	})
	if errorValue != nil {
		t.Fatalf("expected cron schedule run to succeed: %v", errorValue)
	}
	if !result.DidRun {
		t.Fatal("expected due daily research schedule to run")
	}
	if result.LaunchResult.TurnResult.FinishMessage != "Today's research surfaced three key changes." {
		t.Fatalf("expected daily research reply, got %q", result.LaunchResult.TurnResult.FinishMessage)
	}
	if result.Schedule.LastTaskRunID == "" {
		t.Fatalf("expected launched task run id, got %+v", result.Schedule)
	}
	if result.Schedule.LastRunAt == nil || !result.Schedule.LastRunAt.Equal(runAt) {
		t.Fatalf("expected last run time %s, got %+v", runAt.Format(time.RFC3339), result.Schedule.LastRunAt)
	}
	expectedNextRunAt := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	if result.Schedule.NextRunAt == nil || !result.Schedule.NextRunAt.Equal(expectedNextRunAt) {
		t.Fatalf("expected next run time %s, got %+v", expectedNextRunAt.Format(time.RFC3339), result.Schedule.NextRunAt)
	}
}

type staticScheduleLanguageModel struct {
	content string
}

func (languageModel staticScheduleLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel staticScheduleLanguageModel) GenerateStructuredResponse(_ context.Context, request llm.StructuredResponseRequest) (llm.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		return llm.StructuredResponse{Content: scheduleTurnRouterResponse()}, nil
	}
	return llm.StructuredResponse{Content: languageModel.content}, nil
}

func scheduleTurnRouterResponse() string {
	return `{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"xlow","estimatedMinutes":1,"requestedOutputFormats":null,"responseLanguage":"ko","reason":"scheduled run","userFacingReply":""}`
}

func useScheduleTestLanguageModel(agentKernel *loop.AgentKernel, languageModel staticScheduleLanguageModel) {
	agentKernel.UseLanguageModelProvider(languageModel)
	agentKernel.UseIntakeLanguageModelProvider(languageModel)
	agentKernel.UseIntakeOptions(agentcontract.IntakeOptions{IsEnabled: true})
}

func scheduleFinishMessage(reply string) string {
	return `{"action":"reply","final":true,"message":"` + reply + `","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":[],"qualityReview":[]}`
}
