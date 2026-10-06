package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

func TestAskInputUsesTypedQuestionAndResultData(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "Need an answer")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask_input"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	toolContext := toolcontract.WithUserFacingMessage(toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID), "Context question must not replace input")
	result, errorValue := toolRegistry.Invoke(toolContext, toolcontract.ToolInvocation{
		ToolName: "ask_input",
		Input: toolcontract.MarshalToolInput(map[string]any{
			"question": "Which report should I use?",
		}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failed() {
		t.Fatalf("expected ask_input success, got %s", result.ContentText())
	}
	var resultDocument askInputResult
	if errorValue := json.Unmarshal(result.Output.Data, &resultDocument); errorValue != nil {
		t.Fatal(errorValue)
	}
	if resultDocument.Question != "Which report should I use?" || resultDocument.Status != string(task.TaskStatusWaitingUserInput) || len(resultDocument.Options) != 0 {
		t.Fatalf("expected typed ask_input result, got %+v", resultDocument)
	}
}

func askWithChoices(t *testing.T, recordAnswer func(*task.TaskRunService, string)) (toolcontract.ToolResult, *task.TaskRunService, string) {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "conversation-1", "Need an answer")
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseTaskRunService(taskRunService)
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask_input"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})
	recordAnswer(taskRunService, taskRun.TaskRunID)

	result, errorValue := toolRegistry.Invoke(toolcontract.WithTaskRunID(context.Background(), taskRun.TaskRunID), toolcontract.ToolInvocation{
		ToolName: "ask_input",
		Input:    toolcontract.MarshalToolInput(map[string]any{"question": "Which report?", "choices": []string{"First", "Second"}}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return result, taskRunService, taskRun.TaskRunID
}

func TestAskInputWithChoicesAnswersWithTheChoiceTheRequesterPicked(t *testing.T) {
	result, taskRunService, taskRunID := askWithChoices(t, func(taskRunService *task.TaskRunService, taskRunID string) {
		approvalrecord.RecordChoiceAnswer(taskRunService, taskRunID, holdrecord.Choice{Key: "2", Label: "Second"})
	})

	if result.Failed() || !strings.Contains(result.ContentText(), "Second") {
		t.Fatalf("the pick is the answer, got %+v", result)
	}
	if taskRun, _ := taskRunService.FindTaskRun(taskRunID); taskRun.Status == task.TaskStatusWaitingUserInput {
		t.Fatal("a question with choices is held by the approval gate, not paused by the tool")
	}
}

func TestAskInputWithChoicesFailsWhenNoChoiceWasPicked(t *testing.T) {
	result, _, _ := askWithChoices(t, func(*task.TaskRunService, string) {})

	if !result.Failed() {
		t.Fatalf("a question nobody answered has no answer to return, got %+v", result)
	}
}

func TestAskInputRejectsUnknownInput(t *testing.T) {
	toolCatalogBuilder := NewToolCatalogBuilder()
	toolCatalogBuilder.UseAllowedToolNamesByProfile(nil, []string{"ask_input"})
	toolRegistry := toolCatalogBuilder.BuildToolSet(ToolCatalogRequest{ProfileName: "default"})

	result, errorValue := toolRegistry.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: "ask_input",
		Input:    toolcontract.MarshalToolInput(map[string]any{"question": "Continue?", "extra": true}),
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !result.Failed() || result.FailureStage() != "tool_input_schema" {
		t.Fatalf("expected unknown ask_input field to fail schema validation, got %+v", result)
	}
}
