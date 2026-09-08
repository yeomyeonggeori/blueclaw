package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
)

type askInputToolInput struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

type askInputResult struct {
	TaskRunID string                              `json:"taskRunID"`
	Status    string                              `json:"status"`
	Question  string                              `json:"question"`
	Kind      string                              `json:"kind"`
	Options   []agentcontract.ClarificationOption `json:"options"`
}

var (
	askInputSchema       = json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","minLength":1,"pattern":"\\S"},"choices":{"type":"array","items":{"type":"string","minLength":1,"pattern":"\\S"},"uniqueItems":true}},"required":["question"],"additionalProperties":false}`)
	askInputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	askInputResultSchema = json.RawMessage(`{"type":"object","properties":{"taskRunID":{"type":"string","minLength":1,"pattern":"\\S"},"status":{"const":"waiting_user_input"},"question":{"type":"string","minLength":1,"pattern":"\\S"},"kind":{"const":"ask_input"},"options":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string","minLength":1,"pattern":"\\S"},"label":{"type":"string","minLength":1,"pattern":"\\S"},"value":{"type":"string","minLength":1,"pattern":"\\S"}},"required":["key","label","value"],"additionalProperties":false}}},"required":["taskRunID","status","question","kind","options"],"additionalProperties":false}`)
)

func (toolCatalogBuilder *ToolCatalogBuilder) registerAskInputTool(toolRegistry *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[askInputToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        "ask_input",
			Description: "Pause the current task only when the typed outcome contract or a structured tool failure says user input is required. The nonblank question field is authoritative. Use choices=[] for free-form input, or provide choices to let the user pick one of them or type a different answer.",
			InputSchema: askInputSchema,
		},
		Handler: toolCatalogBuilder.askInputTool,
		Result:  toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) askInputTool(toolContext context.Context, input askInputToolInput) (toolcontract.ToolResult, error) {
	taskRunID := toolcontract.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "ask_input", "ask_input requires an active task run"), nil
	}
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "ask_input", "ask_input requires a nonblank question"), nil
	}
	_, errorValue := toolCatalogBuilder.taskRunService.PauseTaskRun(taskRunID, task.TaskStatusWaitingUserInput, question)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "ask_input", errorValue.Error()), nil
	}
	options := numberedClarificationOptions(trimNonEmptyStrings(input.Choices))
	askRequest := agentcontract.NewAskInputRequest(question, options, toolcontract.ResponseLanguageFromContext(toolContext))
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, marshalToolResult(askRequest))
	resultDocument := json.RawMessage(marshalToolResult(askInputResult{
		TaskRunID: taskRunID,
		Status:    string(task.TaskStatusWaitingUserInput),
		Question:  question,
		Kind:      askRequest.Kind,
		Options:   options,
	}))
	return toolcontract.ToolSuccessData(string(resultDocument), resultDocument), nil
}

func numberedClarificationOptions(choices []string) []agentcontract.ClarificationOption {
	options := make([]agentcontract.ClarificationOption, 0, len(choices))
	for index, choice := range choices {
		options = append(options, agentcontract.ClarificationOption{
			Key:   strconv.Itoa(index + 1),
			Label: choice,
			Value: choice,
		})
	}
	return options
}
