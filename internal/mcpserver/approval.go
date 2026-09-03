package mcpserver

import (
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type ApprovalDecision string

const (
	ApprovalDecisionApproved     ApprovalDecision = "approved"
	ApprovalDecisionHeld         ApprovalDecision = "held"
	ApprovalDecisionRejected     ApprovalDecision = "rejected"
	ApprovalDecisionUnanswerable ApprovalDecision = "unanswerable"

	ApprovalDecisionUnresolvedTarget ApprovalDecision = "unresolved_target"
)

type ApprovalRequest struct {
	RequesterPersonID string
	RequesterEmail    string
	TaskRunID         string
	ToolName          string
	ToolInput         json.RawMessage
	ApprovalScope     string
	SideEffectClass   string
	ResponseLanguage  string
	Prompt            string
	Platform          string
	ConversationID    string
	ReplyTargetID     string
	ModelDraft        string
	HarnessSession    HarnessSession
}

type HarnessSession = agentcontract.HarnessSession

type ApprovalOutcome struct {
	Decision ApprovalDecision
	Notice   string
	Failure  toolcontract.ToolResult
}
