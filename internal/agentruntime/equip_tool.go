package agentruntime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

var equipInputSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["need"],
	"properties":{
		"need":{"type":"string","description":"What you need a tool for, in your own words, such as \"a tool that moves a deal to another stage\""}
	}
}`)

var equipResultSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["selectedTools"],
	"properties":{
		"selectedTools":{
			"type":"array",
			"items":{
				"type":"object",
				"additionalProperties":false,
				"required":["name","description"],
				"properties":{
					"name":{"type":"string"},
					"description":{"type":"string"}
				}
			}
		}
	}
}`)

type equipToolInput struct {
	Need string `json:"need"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerEquipTool(toolRegistry *toolcontract.ToolSet, availableToolSet *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[equipToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.EquipToolName,
			Description: "Describe in one sentence what you need a tool to do, and get back the tools that do it with a one-line description each. They become callable on your next step. Say the need, not a tool name: you are not expected to know what anything is called.",
			InputSchema: equipInputSchema,
		},
		Handler: func(toolContext context.Context, input equipToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.equip(toolContext, input, availableToolSet)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) equip(toolContext context.Context, input equipToolInput, availableToolSet *toolcontract.ToolSet) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.toolSelector == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, toolcontract.EquipToolName, "no tool selector is configured for this runtime"), nil
	}
	if strings.TrimSpace(input.Need) == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, toolcontract.EquipToolName, "need must say what the tool has to do"), nil
	}
	callLedger := &agentcontract.IntakeCallLedger{}
	selectedTools, errorValue := toolCatalogBuilder.toolSelector.SelectToolNames(toolContext, agentcontract.ToolSelectionNeed{
		Need:       input.Need,
		ToolSet:    availableToolSet,
		CallLedger: callLedger,
	})
	toolCatalogBuilder.appendSelectionCallRecords(toolContext, callLedger.Records)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, toolcontract.EquipToolName, "tool selection failed: "+errorValue.Error()), nil
	}
	document := json.RawMessage(MarshalBody(agentcontract.EquippedTools{SelectedTools: selectedTools}))
	return toolcontract.ToolSuccessData(foundToolsSummary(selectedTools), document), nil
}

func (toolCatalogBuilder *ToolCatalogBuilder) appendSelectionCallRecords(toolContext context.Context, records []agentcontract.LLMCallRecord) {
	taskRunID := toolcontract.TaskRunIDFromContext(toolContext)
	if taskRunID == "" || toolCatalogBuilder.taskRunService == nil {
		return
	}
	for _, record := range records {
		toolCatalogBuilder.taskRunService.AppendLLMCall(taskRunID, record)
	}
}

func foundToolsSummary(selectedTools []agentcontract.SelectedTool) string {
	if len(selectedTools) == 0 {
		return "No tool in the catalog matches that need."
	}
	lines := make([]string, 0, len(selectedTools)+1)
	lines = append(lines, "Callable from your next step:")
	for _, selectedTool := range selectedTools {
		lines = append(lines, "- "+selectedTool.Name+describedToolSuffix(selectedTool.Description))
	}
	return strings.Join(lines, "\n")
}

func describedToolSuffix(description string) string {
	if strings.TrimSpace(description) == "" {
		return ""
	}
	return " — " + strings.TrimSpace(description)
}
