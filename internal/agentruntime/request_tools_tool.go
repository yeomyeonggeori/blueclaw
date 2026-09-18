package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

var requestToolsInputSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["toolNames"],
	"properties":{
		"toolNames":{
			"type":"array",
			"items":{"type":"string"},
			"description":"Exact tool names to load, taken from the additional-available-tools list"
		}
	}
}`)

var requestToolsResultSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["requestedToolNames"],
	"properties":{
		"requestedToolNames":{"type":"array","items":{"type":"string"}}
	}
}`)

type requestToolsToolInput struct {
	ToolNames []string `json:"toolNames"`
}

type requestToolsToolOutput struct {
	RequestedToolNames []string `json:"requestedToolNames"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerRequestToolsTool(toolRegistry *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[requestToolsToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.RequestToolsToolName,
			Description: "Load additional tools into your palette by exact name. Use only names from the additional-available-tools list in your instructions; the loaded tools become callable on your next step.",
			InputSchema: requestToolsInputSchema,
		},
		Handler: func(_ context.Context, input requestToolsToolInput) (toolcontract.ToolResult, error) {
			document := json.RawMessage(MarshalBody(requestToolsToolOutput{RequestedToolNames: input.ToolNames}))
			return toolcontract.ToolSuccessData(string(document), document), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
}
