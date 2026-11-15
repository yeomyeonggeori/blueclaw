package holdrule

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func MayHold(toolDefinition toolcontract.ToolDefinition) bool {
	return toolDefinition.RequiresApproval ||
		strings.TrimSpace(toolDefinition.Name) == toolcontract.AskInputToolName ||
		AcceptsApprovalRequired(toolDefinition.InputSchema)
}

func AcceptsApprovalRequired(inputSchema json.RawMessage) bool {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil {
		return false
	}
	_, isDeclared := schema.Properties["approvalRequired"]
	return isDeclared
}
