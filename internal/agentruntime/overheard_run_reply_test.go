package agentruntime

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func toolSetDeclaring(t *testing.T, sideEffectClassByToolName map[string]string) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet(nil)
	for toolName, sideEffectClass := range sideEffectClassByToolName {
		definition := toolcontract.ToolDefinition{Name: toolName, SideEffectClass: sideEffectClass}
		handler := func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return toolcontract.ToolResult{}, nil
		}
		if errorValue := toolSet.RegisterTool(definition, handler); errorValue != nil {
			t.Fatalf("register %s: %v", toolName, errorValue)
		}
	}
	return toolSet
}

func toolResultEvent(toolName string, body string) agentcontract.TaskEvent {
	return agentcontract.TaskEvent{Name: agentcontract.ToolTaskEventName(toolName, agentcontract.ToolTaskEventResultSuffix), Body: body}
}

func completedTurn() agentcontract.AgentTurnResult {
	return agentcontract.AgentTurnResult{
		TaskRun:       agentcontract.TaskRun{Status: agentcontract.TaskStatusCompleted},
		FinishMessage: "기록할 일정이 없습니다.",
	}
}

func TestAnOverheardRunThatOnlyReadEndsWithoutAWordToTheRoom(t *testing.T) {
	toolSet := toolSetDeclaring(t, map[string]string{"event_list": toolcontract.ToolSideEffectRead, "event_add": toolcontract.ToolSideEffectStateChange})
	events := []agentcontract.TaskEvent{toolResultEvent("event_list", `{"observationID":"o1","content":"[]"}`)}

	result := quietWhenNothingChanged(toolSet, events, completedTurn())

	if result.ReplySuppressionReason != overheardRunChangedNothing {
		t.Fatalf("expected an overheard run that changed nothing to stay quiet, got %+v", result)
	}
}

func TestAnOverheardRunWhoseWriteFailedChangedNothing(t *testing.T) {
	toolSet := toolSetDeclaring(t, map[string]string{"event_add": toolcontract.ToolSideEffectStateChange})
	events := []agentcontract.TaskEvent{toolResultEvent("event_add", `{"observationID":"o1","failure":{"code":"invalid_input"}}`)}

	result := quietWhenNothingChanged(toolSet, events, completedTurn())

	if result.ReplySuppressionReason != overheardRunChangedNothing {
		t.Fatalf("expected a failed write to count as no change, got %+v", result)
	}
}

func TestAnOverheardRunThatChangedARecordSaysWhatItDid(t *testing.T) {
	toolSet := toolSetDeclaring(t, map[string]string{"event_list": toolcontract.ToolSideEffectRead, "event_add": toolcontract.ToolSideEffectStateChange})
	events := []agentcontract.TaskEvent{
		toolResultEvent("event_list", `{"observationID":"o1","content":"[]"}`),
		toolResultEvent("event_add", `{"observationID":"o2","content":"created"}`),
	}

	result := quietWhenNothingChanged(toolSet, events, completedTurn())

	if result.ReplySuppressed || result.ReplySuppressionReason != "" {
		t.Fatalf("expected a run that changed something to report it, got %+v", result)
	}
}

func TestAToolTheRunDoesNotKnowCountsAsAChange(t *testing.T) {
	events := []agentcontract.TaskEvent{toolResultEvent("unlisted_tool", `{"observationID":"o1","content":"ok"}`)}

	result := quietWhenNothingChanged(toolcontract.NewToolSet(nil), events, completedTurn())

	if result.ReplySuppressed {
		t.Fatal("expected a succeeded call of unknown effect to keep the report rather than hide it")
	}
}

func TestOnlyAFinishedOverheardRunIsConsideredForQuiet(t *testing.T) {
	launcher := &TaskLauncher{}
	overheard := TaskLaunchRequest{}
	overheard.AmbientDuty.IsMatch = true
	failed := agentcontract.AgentTurnResult{TaskRun: agentcontract.TaskRun{Status: agentcontract.TaskStatusFailed}, UserNotice: "실패했습니다."}

	if result := launcher.withOverheardRunQuietWhenNothingChanged(TaskLaunchRequest{}, nil, completedTurn()); result.ReplySuppressed {
		t.Fatal("expected an addressed run to always answer")
	}
	if result := launcher.withOverheardRunQuietWhenNothingChanged(overheard, nil, failed); result.ReplySuppressed {
		t.Fatal("expected a failed overheard run to still say it failed")
	}
}
