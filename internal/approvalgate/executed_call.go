package approvalgate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func RecordApprovalSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, toolName string, toolInput json.RawMessage) string {
	approvedHold, _ := approvedHoldForCall(holdsOf(taskRunStore.ListTaskEvent(taskRunID)), toolName, toolInput)
	recordSpent(taskRunStore, taskRunID, approvedHold.ID, toolName, toolInput)
	return approvedHold.ID
}

func RecordApprovedCallSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, approvedCall ApprovedCall) {
	recordSpent(taskRunStore, taskRunID, approvedCall.HoldID, approvedCall.ToolName, approvedCall.ToolInput)
}

func recordSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, holdID string, toolName string, toolInput json.RawMessage) {
	body := spentApprovalBody(taskRunStore, taskRunID, toolName, toolInput)
	body["holdID"] = holdID
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalExecuted, marshalEventBody(body))
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

func unspentHeldCallToken(taskEvents []agentcontract.TaskEvent, toolName string) string {
	trimmedToolName := strings.TrimSpace(toolName)
	mintedTokens := []string{}
	spentTokens := map[string]bool{}
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHeldCall:
			heldCall := decodeHeldCallEventBody(taskEvent.Body)
			if heldCall.ToolName == trimmedToolName && heldCall.ApprovalToken != "" {
				mintedTokens = append(mintedTokens, heldCall.ApprovalToken)
			}
		case agentcontract.TaskEventApprovalExecuted:
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

func HeldCallID(toolName string, toolInput json.RawMessage) string {
	digest := sha256.Sum256([]byte(agentcontract.CanonicalToolCallKey(toolName, toolInput)))
	return "held-" + hex.EncodeToString(digest[:8])
}
