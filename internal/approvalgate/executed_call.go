package approvalgate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func RecordApprovalSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, toolName string, toolInput json.RawMessage) string {
	approvedHold, _ := approvalrecord.ApprovedHoldForCall(approvalrecord.Holds(taskRunStore.ListTaskEvent(taskRunID)), toolName, toolInput)
	approvalrecord.Spend(taskRunStore, taskRunID, approvedHold.ID, toolName, toolInput)
	return approvedHold.ID
}

func RecordApprovedCallSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, approvedCall ApprovedCall) {
	approvalrecord.Spend(taskRunStore, taskRunID, approvedCall.HoldID, approvedCall.ToolName, approvedCall.ToolInput)
}

func HeldCallID(toolName string, toolInput json.RawMessage) string {
	digest := sha256.Sum256([]byte(agentcontract.CanonicalToolCallKey(toolName, toolInput)))
	return "held-" + hex.EncodeToString(digest[:8])
}
