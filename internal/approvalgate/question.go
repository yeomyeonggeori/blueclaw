package approvalgate

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func OffersChoices(toolDefinition toolcontract.ToolDefinition, toolInput json.RawMessage) bool {
	return len(AskedChoices(toolDefinition.Name, toolInput)) > 0
}

func (gate *Gate) questionToAsk(approvalRequest mcpserver.ApprovalRequest) (ApprovalTargetResolution, string, bool) {
	choices := AskedChoices(approvalRequest.ToolName, approvalRequest.ToolInput)
	if len(choices) == 0 {
		return ApprovalTargetResolution{}, "", false
	}
	lines := []string{AskedQuestion(approvalRequest.ToolInput)}
	for index, choice := range choices {
		lines = append(lines, strconv.Itoa(index+1)+". "+choice.Label)
	}
	return ApprovalTargetResolution{Choices: choices}, strings.Join(lines, "\n"), true
}
