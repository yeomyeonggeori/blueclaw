package agentruntime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type ToolSelector interface {
	SelectToolNames(context.Context, agentcontract.ToolSelectionNeed) ([]agentcontract.SelectedTool, error)
}

var findToolsInputSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["need"],
	"properties":{
		"need":{"type":"string","description":"What you need a tool for, in your own words, such as \"a tool that moves a deal to another stage\""}
	}
}`)

var findToolsResultSchema = json.RawMessage(`{
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

type findToolsToolInput struct {
	Need string `json:"need"`
}

type findToolsToolOutput struct {
	SelectedTools []agentcontract.SelectedTool `json:"selectedTools"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerFindToolsTool(toolRegistry *toolcontract.ToolSet, availableToolSet *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[findToolsToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.FindToolsToolName,
			Description: "Describe in one sentence what you need a tool to do, and get back the tools that do it with a one-line description each. They become callable on your next step. Say the need, not a tool name: you are not expected to know what anything is called.",
			InputSchema: findToolsInputSchema,
		},
		Handler: func(toolContext context.Context, input findToolsToolInput) (toolcontract.ToolResult, error) {
			return toolCatalogBuilder.findTools(toolContext, input, availableToolSet)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func (toolCatalogBuilder *ToolCatalogBuilder) findTools(toolContext context.Context, input findToolsToolInput, availableToolSet *toolcontract.ToolSet) (toolcontract.ToolResult, error) {
	if toolCatalogBuilder.toolSelector == nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, toolcontract.FindToolsToolName, "no tool selector is configured for this runtime"), nil
	}
	if strings.TrimSpace(input.Need) == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, toolcontract.FindToolsToolName, "need must say what the tool has to do"), nil
	}
	selectedTools, errorValue := toolCatalogBuilder.toolSelector.SelectToolNames(toolContext, agentcontract.ToolSelectionNeed{
		Need:    input.Need,
		ToolSet: availableToolSet,
	})
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, toolcontract.FindToolsToolName, "tool selection failed: "+errorValue.Error()), nil
	}
	document := json.RawMessage(MarshalBody(findToolsToolOutput{SelectedTools: selectedTools}))
	return toolcontract.ToolSuccessData(foundToolsSummary(selectedTools), document), nil
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
