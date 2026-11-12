package approvalgate

import (
	"encoding/json"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func OffersChoices(toolDefinition toolcontract.ToolDefinition, toolInput json.RawMessage) bool {
	return len(AskedChoices(toolDefinition.Name, toolInput)) > 0
}

func (gate *Gate) questionToAsk(approvalRequest mcpserver.ApprovalRequest) (ApprovalTargetResolution, string, bool) {
	choices := AskedChoices(approvalRequest.ToolName, approvalRequest.ToolInput)
	if len(choices) == 0 {
		return ApprovalTargetResolution{}, "", false
	}
	return ApprovalTargetResolution{Choices: choices}, AskedQuestion(approvalRequest.ToolInput), true
}
