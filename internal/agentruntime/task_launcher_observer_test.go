//go:build !nobundledharness

package agentruntime

import (
	"context"
	"slices"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type toolCallingHarness struct {
	taskEvents     *task.TaskEventService
	movedTaskRunID string
	ranOnTaskRunID string
}

func (harness *toolCallingHarness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if observer := toolcallprogress.ObserverFrom(ctx); observer != nil {
		observer(acp.StartToolCall("own-call", "harness own tool"))
	}
	harness.runTool(request.ExistingTaskRunID, "before_move")
	harness.ranOnTaskRunID = request.ExistingTaskRunID
	if harness.movedTaskRunID != "" {
		request.TaskRunChosen(harness.movedTaskRunID)
		harness.runTool(request.ExistingTaskRunID, "abandoned")
		harness.ranOnTaskRunID = harness.movedTaskRunID
		harness.runTool(harness.movedTaskRunID, "after_move")
	}
	return agentcontract.AgentTurnResult{TaskRun: agentcontract.TaskRun{TaskRunID: harness.ranOnTaskRunID}}, nil
}

func (harness *toolCallingHarness) runTool(taskRunID string, toolName string) {
	body := `{"observationID":"` + toolName + `","input":{"path":"/` + toolName + `"}}`
	harness.taskEvents.AppendTaskEvent(taskRunID, "tool."+toolName+".requested", body)
	harness.taskEvents.AppendTaskEvent(taskRunID, "tool."+toolName+".result", `{"observationID":"`+toolName+`"}`)
}

func describeToolCallUpdate(update acp.SessionUpdate) string {
	if update.ToolCall != nil {
		return "tool_call " + update.ToolCall.Title
	}
	return "tool_call_update " + string(update.ToolCallUpdate.ToolCallId)
}

type observedLaunch struct {
	taskEvents *task.TaskEventService
	taskRunID  string
	observed   []string
}

type launchShape struct {
	continuesTaskRun bool
	movesToTaskRun   bool
}

func launchObservingToolCalls(t *testing.T, shape launchShape) *observedLaunch {
	t.Helper()
	launch := &observedLaunch{taskEvents: task.NewTaskEventService()}
	taskRuns := task.NewTaskRunService(launch.taskEvents)
	harness := &toolCallingHarness{taskEvents: launch.taskEvents}
	request := TaskLaunchRequest{
		Source:                    TaskLaunchSourceConnector,
		RequesterPersonID:         "person-1",
		ProfileName:               "default",
		ConversationID:            "channel-1",
		Prompt:                    "발표자료 만들어줘",
		HistoryProvider:           staticHistoryProvider{},
		PersonAccess:              policy.PersonAccess{PersonID: "person-1"},
		AccessibleConversationIDs: []string{"channel-1"},
		ToolCallObserver: func(update acp.SessionUpdate) {
			launch.observed = append(launch.observed, describeToolCallUpdate(update))
		},
	}
	if shape.continuesTaskRun {
		request.ExistingTaskRunID = taskRuns.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "channel-1"}, request.Prompt).TaskRunID
	}
	if shape.movesToTaskRun {
		harness.movedTaskRunID = taskRuns.CreateTaskRunWithOrigin("person-1", task.TaskRunOrigin{ConversationID: "channel-1"}, request.Prompt).TaskRunID
	}
	launchResult, errorValue := routedTaskLauncher(harness, taskRuns, NewToolCatalogBuilder(), staticRuntimeLanguageModel{content: runtimeFinishMessage("done")}).Launch(context.Background(), request)
	if errorValue != nil {
		t.Fatalf("launch: %v", errorValue)
	}
	launch.taskRunID = launchResult.TurnResult.TaskRun.TaskRunID
	return launch
}

func TestTaskLauncherTellsTheObserverAboutToolCallsOnTheRunItOpens(t *testing.T) {
	launch := launchObservingToolCalls(t, launchShape{})

	expected := []string{"tool_call harness own tool", "tool_call before_move(/before_move)", "tool_call_update before_move"}
	if !slices.Equal(launch.observed, expected) {
		t.Fatalf("the observer was told %q, expected %q", launch.observed, expected)
	}
}

func TestTaskLauncherTellsTheObserverAboutToolCallsOnARunItContinues(t *testing.T) {
	launch := launchObservingToolCalls(t, launchShape{continuesTaskRun: true})

	if len(launch.observed) != 3 {
		t.Fatalf("the observer was told %q, expected the harness's own call and the one call of the continued run", launch.observed)
	}
}

func TestTaskLauncherFollowsATurnTheKernelMovesOntoAnotherRun(t *testing.T) {
	launch := launchObservingToolCalls(t, launchShape{movesToTaskRun: true})

	expected := []string{
		"tool_call harness own tool",
		"tool_call before_move(/before_move)", "tool_call_update before_move",
		"tool_call after_move(/after_move)", "tool_call_update after_move",
	}
	if !slices.Equal(launch.observed, expected) {
		t.Fatalf("the observer was told %q, expected %q: the opened run until the move, then only the run moved onto, each call once", launch.observed, expected)
	}
}

func TestTaskLauncherStopsTellingTheObserverWhenTheTurnEnds(t *testing.T) {
	launch := launchObservingToolCalls(t, launchShape{})
	told := len(launch.observed)

	launch.taskEvents.AppendTaskEvent(launch.taskRunID, "tool.late.requested", `{"observationID":"late"}`)

	if len(launch.observed) != told {
		t.Fatal("the observer kept being told about the run after the turn ended")
	}
}
