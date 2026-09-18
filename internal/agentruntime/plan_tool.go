package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

var planUpdateInputSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["steps"],
	"properties":{
		"goal":{"type":"string"},
		"steps":{
			"type":"array",
			"items":{
				"type":"object",
				"additionalProperties":false,
				"required":["title","status"],
				"properties":{
					"title":{"type":"string"},
					"status":{"type":"string","enum":["pending","in_progress","done","skipped"]}
				}
			}
		}
	}
}`)

var planUpdateResultSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["steps"],
	"properties":{
		"goal":{"type":"string"},
		"steps":{
			"type":"array",
			"items":{
				"type":"object",
				"additionalProperties":false,
				"required":["title","status"],
				"properties":{
					"title":{"type":"string"},
					"status":{"type":"string","enum":["pending","in_progress","done","skipped"]}
				}
			}
		}
	}
}`)

type planUpdateToolInput struct {
	Goal  string                  `json:"goal"`
	Steps []toolcontract.PlanStep `json:"steps"`
}

type planUpdateToolOutput struct {
	Goal  string                  `json:"goal,omitempty"`
	Steps []toolcontract.PlanStep `json:"steps"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerPlanUpdateTool(toolRegistry *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[planUpdateToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.PlanUpdateToolName,
			Description: "Record or update your goal and step plan for this task. Send the FULL current list every time (it replaces the previous plan). Keep statuses current as you work; revising the plan is normal and never an error.",
			InputSchema: planUpdateInputSchema,
		},
		Handler: func(_ context.Context, input planUpdateToolInput) (toolcontract.ToolResult, error) {
			goal, steps := toolcontract.NormalizePlan(input.Goal, input.Steps)
			document := json.RawMessage(MarshalBody(planUpdateToolOutput{Goal: goal, Steps: steps}))
			return toolcontract.ToolSuccessData(string(document), document), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
}
