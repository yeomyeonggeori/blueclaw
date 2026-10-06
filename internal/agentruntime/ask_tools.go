package agentruntime

import (
	"context"
	"encoding/json"
	"strconv"
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
	TaskRunID string                              `json:"taskRunID"`
	Status    string                              `json:"status"`
	Question  string                              `json:"question"`
	Kind      string                              `json:"kind"`
	Options   []agentcontract.ClarificationOption `json:"options"`
}

var (
	askInputSchema       = json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","minLength":1,"pattern":"\\S"},"choices":{"type":"array","items":{"type":"string","minLength":1,"pattern":"\\S"},"uniqueItems":true}},"required":["question"],"additionalProperties":false}`)
	askInputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	askInputResultSchema = json.RawMessage(`{"type":"object","properties":{"taskRunID":{"type":"string","minLength":1,"pattern":"\\S"},"status":{"type":"string","enum":["waiting_user_input","answered"]},"question":{"type":"string","minLength":1,"pattern":"\\S"},"kind":{"const":"ask_input"},"choiceKey":{"type":"string","minLength":1},"answer":{"type":"string","minLength":1},"options":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string","minLength":1,"pattern":"\\S"},"label":{"type":"string","minLength":1,"pattern":"\\S"},"value":{"type":"string","minLength":1,"pattern":"\\S"}},"required":["key","label","value"],"additionalProperties":false}}},"required":["status","question"],"additionalProperties":false}`)
)

func (toolCatalogBuilder *ToolCatalogBuilder) registerAskInputTool(toolRegistry *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[askInputToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.AskInputToolName,
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
	options := numberedClarificationOptions(trimNonEmptyStrings(input.Choices))
	askRequest := agentcontract.NewAskInputRequest(question, options, toolcontract.ResponseLanguageFromContext(toolContext))
	toolCatalogBuilder.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, MarshalBody(askRequest))
	resultDocument := json.RawMessage(MarshalBody(askInputResult{
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
