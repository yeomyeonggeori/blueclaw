package approvalgate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type ApprovalTarget struct {
	InputField string `json:"inputField,omitempty"`
	ID         string `json:"id,omitempty"`
	Title      string `json:"title,omitempty"`
	StartsAt   string `json:"startsAt,omitempty"`
	// What the action is about to touch, quoted for the approval question.
	// A preview never narrows the replayed input the way a resolved ID does.
	Preview string `json:"preview,omitempty"`
}

type ApprovalTargetResolution struct {
	Target  ApprovalTarget
	Failure toolcontract.ToolResult
}

type ApprovalTargetRequest struct {
	ToolName          string
	ToolInput         json.RawMessage
	RequesterPersonID string
	RequesterEmail    string
	Platform          string
	ConversationID    string
	ReplyTargetID     string
}

type ApprovalTargetResolver interface {
	ResolveApprovalTarget(context.Context, ApprovalTargetRequest) (ApprovalTargetResolution, error)
}

func (gate *Gate) UseApprovalTargetResolver(approvalTargetResolver ApprovalTargetResolver) {
	gate.approvalTargetResolver = approvalTargetResolver
}

func (target ApprovalTarget) isResolved() bool {
	return strings.TrimSpace(target.InputField) != "" && strings.TrimSpace(target.ID) != ""
}

func (resolution ApprovalTargetResolution) namesNothingThatExists() bool {
	return !resolution.Target.isResolved() && resolution.Failure.Failure != nil
}

func (gate *Gate) resolveApprovalTarget(ctx context.Context, approvalRequest mcpserver.ApprovalRequest) ApprovalTargetResolution {
	if gate.approvalTargetResolver == nil {
		return ApprovalTargetResolution{}
	}
	resolution, errorValue := gate.approvalTargetResolver.ResolveApprovalTarget(ctx, ApprovalTargetRequest{
		ToolName:          strings.TrimSpace(approvalRequest.ToolName),
		ToolInput:         approvalRequest.ToolInput,
		RequesterPersonID: approvalRequest.RequesterPersonID,
		RequesterEmail:    approvalRequest.RequesterEmail,
		Platform:          approvalRequest.Platform,
		ConversationID:    approvalRequest.ConversationID,
		ReplyTargetID:     approvalRequest.ReplyTargetID,
	})
	if errorValue == nil {
		return resolution
	}
	slog.Warn("approvalgate.target_resolution_unreachable",
		"toolName", strings.TrimSpace(approvalRequest.ToolName),
		"taskRunID", strings.TrimSpace(approvalRequest.TaskRunID),
		"reason", errorValue.Error())
	return ApprovalTargetResolution{}
}

func narrowedToolInput(toolInput json.RawMessage, target ApprovalTarget) json.RawMessage {
	if !target.isResolved() {
		return nil
	}
	document := map[string]json.RawMessage{}
	if json.Unmarshal(toolInput, &document) != nil {
		return nil
	}
	identity, errorValue := json.Marshal(strings.TrimSpace(target.ID))
	if errorValue != nil {
		return nil
	}
	document[strings.TrimSpace(target.InputField)] = identity
	narrowedInput, errorValue := json.Marshal(document)
	if errorValue != nil {
		return nil
	}
	return narrowedInput
}
