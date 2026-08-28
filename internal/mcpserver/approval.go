package mcpserver

import (
	"encoding/json"

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

// HarnessSession is the handle that lets a held call be resumed inside the
// conversation that asked for it, rather than restarting the agent's
// reasoning from nothing. A harness that cannot resume leaves it empty.
type HarnessSession struct {
	HarnessName string `json:"harnessName,omitempty"`
	SessionID   string `json:"sessionID,omitempty"`
	IsResumable bool   `json:"isResumable"`
}

type ApprovalOutcome struct {
	Decision ApprovalDecision
	Notice   string
	Failure  toolcontract.ToolResult
}
