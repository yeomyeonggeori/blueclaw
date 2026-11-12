package agentruntime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type askInputToolInput struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

type askInputResult struct {
	TaskRunID string `json:"taskRunID"`
	Status    string `json:"status"`
	Question  string `json:"question"`
	Kind      string `json:"kind"`
}

var (
	askInputSchema       = json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","minLength":1,"pattern":"\\S"},"choices":{"type":"array","items":{"type":"string","minLength":1,"pattern":"\\S"},"uniqueItems":true}},"required":["question"],"additionalProperties":false}`)
	askInputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	askInputResultSchema = json.RawMessage(`{"type":"object","properties":{"taskRunID":{"type":"string","minLength":1,"pattern":"\\S"},"status":{"type":"string","enum":["waiting_user_input","answered"]},"question":{"type":"string","minLength":1,"pattern":"\\S"},"kind":{"const":"ask_input"},"choiceKey":{"type":"string","minLength":1},"answer":{"type":"string","minLength":1}},"required":["status","question"],"additionalProperties":false}`)
)

func (toolCatalogBuilder *ToolCatalogBuilder) registerAskInputTool(toolRegistry *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[askInputToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.AskInputToolName,
			Description: "Ask the person a question and pause until they answer. Use it when the outcome contract or a structured tool failure requires their input, and whenever you would offer options: pass them as choices, which the conversation shows as a numbered pressable list, so never repeat them in reply text. choices=[] asks for a free-form answer; the person may always answer in their own words.",
			InputSchema: askInputSchema,
		},
		Handler: toolCatalogBuilder.askInputTool,
		Result:  toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) askInputTool(toolContext context.Context, input askInputToolInput) (toolcontract.ToolResult, error) {
	taskRunID := toolcontract.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, toolcontract.AskInputToolName, "ask_input requires an active task run"), nil
	}
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, toolcontract.AskInputToolName, "ask_input requires a nonblank question"), nil
	}
	if len(trimNonEmptyStrings(input.Choices)) > 0 {
		return approvalgate.ChosenAnswerResult(toolCatalogBuilder.taskRunService.ListTaskEvent(taskRunID), json.RawMessage(MarshalBody(input))), nil
	}
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, question)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, toolcontract.AskInputToolName, errorValue.Error()), nil
	}
	askRequest := agentcontract.NewAskInputRequest(question, nil, toolcontract.ResponseLanguageFromContext(toolContext))
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, MarshalBody(askRequest))
	resultDocument := json.RawMessage(MarshalBody(askInputResult{
		TaskRunID: taskRunID,
		Status:    string(task.TaskStatusWaitingUserInput),
		Question:  question,
		Kind:      askRequest.Kind,
	}))
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}
