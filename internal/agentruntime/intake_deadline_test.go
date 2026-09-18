package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/launchfailure"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type deadlineBlockingRouterLanguageModel struct{}

func (deadlineBlockingRouterLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("router model only serves structured routing")
}

func (deadlineBlockingRouterLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ model.StructuredResponseRequest) (model.StructuredResponse, error) {
	<-responseContext.Done()
	return model.StructuredResponse{}, responseContext.Err()
}

func TestTaskLauncherRouterDeadlinePersistsOneBlockedTask(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	taskLauncher := NewTaskLauncher(harnesstest.New(taskRunService), taskRunService, NewToolCatalogBuilder())
	taskLauncher.UseTurnRouter(intake.NewTurnRouter(deadlineBlockingRouterLanguageModel{}, intake.NewDecisionPlanner(&intaketest.LanguageModelDecisionModel{LanguageModel: deadlineBlockingRouterLanguageModel{}}, nil, nil), agentcontract.IntakeOptions{IsEnabled: true}))
	taskLauncher.UseIntakeBudget(IntakeBudget{TaskLevel: string(agentcontract.TaskLevelLow), MaxElapsedSecond: 1})
	taskLauncher.UseLaunchFailureCompleter(launchfailure.NewCompleter(taskRunService, nil))

	launchResult, errorValue := taskLauncher.Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "고객지원 업무를 정리해줘",
		ResponseLanguage:  "ko",
		TurnStartedAt:     time.Now().Add(-2 * time.Second),
		PersonAccess:      policy.PersonAccess{PersonID: "person-1", SecurityLevelRank: 100},
	})

	if errorValue != nil {
		t.Fatalf("expected persisted max elapsed result: %v", errorValue)
	}
	taskRuns := taskRunService.ListTaskRunByPersonID("person-1")
	if len(taskRuns) != 1 || launchResult.TurnResult.TaskRun.TaskRunID != taskRuns[0].TaskRunID {
		t.Fatalf("expected exactly one persisted task, got %+v", taskRuns)
	}
	if launchResult.TurnResult.TaskRun.Status != task.TaskStatusBlocked || launchResult.TurnResult.TaskRun.FailureReason != "max_elapsed" {
		t.Fatalf("expected blocked max elapsed task, got %+v", launchResult.TurnResult.TaskRun)
	}
	taskEvents := taskRunService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	if countTaskEventsNamed(taskEvents, "agent.limit_stop") != 1 || countTaskEventsNamed(taskEvents, "agent.goal.blocked") != 1 {
		t.Fatalf("expected one limit and goal event, got %+v", taskEvents)
	}
	if !taskEventsCarry(taskEvents, "agent.limit_stop", `"phase":"intake"`) {
		t.Fatal("expected observable intake max elapsed event")
	}
	if countTaskEventsNamed(taskEvents, "agent.intake") != 0 {
		t.Fatal("a router deadline must not invent an intake decision")
	}
}

func countTaskEventsNamed(taskEvents []task.TaskEvent, name string) int {
	count := 0
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name {
			count++
		}
	}
	return count
}

func taskEventsCarry(taskEvents []task.TaskEvent, name string, fragment string) bool {
	for _, taskEvent := range taskEvents {
		if taskEvent.Name == name && strings.Contains(taskEvent.Body, fragment) {
			return true
		}
	}
	return false
}
