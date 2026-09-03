package approvalgate

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func RecordApprovalSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, toolName string, toolInput json.RawMessage) {
	taskRunStore.AppendTaskEvent(taskRunID, taskstate.TaskEventApprovalExecuted, marshalEventBody(spentApprovalBody(taskRunStore, taskRunID, toolName, toolInput)))
}

func spentApprovalBody(taskRunStore taskstate.TaskRunStore, taskRunID string, toolName string, toolInput json.RawMessage) map[string]any {
	body := map[string]any{"toolName": strings.TrimSpace(toolName)}
	if len(toolInput) > 0 {
		body["toolInput"] = toolInput
	}
	if approvalToken := unspentHeldCallToken(taskRunStore.ListTaskEvent(taskRunID), toolName); approvalToken != "" {
		body["approvalToken"] = approvalToken
	}
	return body
}

func unspentHeldCallToken(taskEvents []taskstate.TaskEvent, toolName string) string {
	trimmedToolName := strings.TrimSpace(toolName)
	mintedTokens := []string{}
	spentTokens := map[string]bool{}
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case taskstate.TaskEventApprovalHeldCall:
			heldCall := decodeHeldCallEventBody(taskEvent.Body)
			if heldCall.ToolName == trimmedToolName && heldCall.ApprovalToken != "" {
				mintedTokens = append(mintedTokens, heldCall.ApprovalToken)
			}
		case taskstate.TaskEventApprovalExecuted:
			spentTokens[decodeHeldCallEventBody(taskEvent.Body).ApprovalToken] = true
		}
	}
	for _, approvalToken := range mintedTokens {
		if !spentTokens[approvalToken] {
			return approvalToken
		}
	}
	return ""
}
