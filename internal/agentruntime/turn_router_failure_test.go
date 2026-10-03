package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
)

func TestTaskLauncherPersistsTurnRouterFailureWithoutFallbackRoute(t *testing.T) {
	taskEventService := task.NewTaskEventService()
	taskRunService := task.NewTaskRunService(taskEventService)
	failingRouterLanguageModel := failingRuntimeRouterLanguageModel{errorValue: errors.New("router unavailable")}
	failureNoticeLanguageModel := authoredRuntimeFailureLanguageModel{reply: "요청을 분류하지 못해 이번 작업을 시작하지 못했습니다. 다시 요청해 주세요."}

	launchResult, errorValue := routedTaskLauncherAuthoringNoticesWith(
		harnesstest.New(taskRunService),
		taskRunService,
		NewToolCatalogBuilder(),
		failingRouterLanguageModel,
		failureNoticeLanguageModel,
	).Launch(context.Background(), TaskLaunchRequest{
		Source:            TaskLaunchSourceConnector,
		RequesterPersonID: "person-1",
		ConversationID:    "channel-1",
		Prompt:            "오늘 무슨 요일이야?",
		PersonAccess:      policy.PersonAccess{PersonID: "person-1"},
	})
	if errorValue != nil {
		t.Fatalf("expected persisted router failure result: %v", errorValue)
	}
	if launchResult.TurnResult.TaskRun.Status != task.TaskStatusFailed || launchResult.TurnResult.FailureNotice.Source != "generated" {
		t.Fatalf("expected LLM-authored failed task, got %+v", launchResult.TurnResult)
	}
	if !strings.Contains(launchResult.TurnResult.FailureNotice.SendableMessage(), "분류하지 못해") {
		t.Fatalf("expected authored failure notice, got %+v", launchResult.TurnResult.FailureNotice)
	}
	taskEvents := taskEventService.ListTaskEvent(launchResult.TurnResult.TaskRun.TaskRunID)
	llmCallEvent := findTaskEvent(taskEvents, "llm.call")
	if llmCallEvent.Name == "" || !strings.Contains(llmCallEvent.Body, `"isError":true`) || !strings.Contains(llmCallEvent.Body, "router unavailable") {
		t.Fatalf("expected persisted router error call, got %+v", taskEvents)
	}
	if containsTaskEvent(taskEvents, "agent.intake") {
		t.Fatal("router failure must not invent an intake decision")
	}
}
