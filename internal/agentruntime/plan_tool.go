package agentruntime

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

var planInputSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["steps"],
	"properties":{
		"goal":{"type":"string"},
		"level":{"type":"string","enum":["low","medium","high","xhigh","max"]},
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

var planResultSchema = json.RawMessage(`{
	"type":"object",
	"additionalProperties":false,
	"required":["steps"],
	"properties":{
		"goal":{"type":"string"},
		"level":{"type":"string"},
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

type planToolInput struct {
	Goal  string                  `json:"goal"`
	Level string                  `json:"level"`
	Steps []toolcontract.PlanStep `json:"steps"`
}

type planToolOutput struct {
	Goal  string                  `json:"goal,omitempty"`
	Level string                  `json:"level,omitempty"`
	Steps []toolcontract.PlanStep `json:"steps"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerPlanTool(toolRegistry *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolRegistry, toolcontract.ToolFunction[planToolInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			Name:        toolcontract.PlanToolName,
			Description: "Record your goal, your step plan and the size of this task. Send the FULL current plan every time (it replaces the previous one). Set level from the steps you just listed: low for a handful of calls, medium for a dozen or so, high when the work runs to several dozen, xhigh or max beyond that; the level decides how much room the task gets, so raise it on a later call when the plan grows. Keep statuses current as you work; revising the plan is normal and never an error.",
			InputSchema: planInputSchema,
		},
		Handler: func(_ context.Context, input planToolInput) (toolcontract.ToolResult, error) {
			goal, steps := toolcontract.NormalizePlan(input.Goal, input.Steps)
			level := string(agentcontract.NormalizeTaskLevel(input.Level))
			document := json.RawMessage(MarshalBody(planToolOutput{Goal: goal, Level: level, Steps: steps}))
			return toolcontract.ToolSuccessData(string(document), document), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
}
