package approvalgate

import (
	"context"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

type ConversationAsker interface {
	PermissionAsker
	HarnessPermissionAsker
	Serves(platform string, conversationID string) bool
}

type routedPermissionAsker struct {
	primary   ConversationAsker
	otherwise PermissionAsker
}

func AskerRoutedBy(primary ConversationAsker, otherwise PermissionAsker) PermissionAsker {
	if otherwise == nil {
		return primary
	}
	return routedPermissionAsker{primary: primary, otherwise: otherwise}
}

func (asker routedPermissionAsker) AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question PermissionQuestion) (ApprovalAnswer, AskStatus) {
	if asker.primary.Serves(approvalRequest.Platform, approvalRequest.ConversationID) {
		return asker.primary.AskPermission(ctx, approvalRequest, question)
	}
	return asker.otherwise.AskPermission(ctx, approvalRequest, question)
}

func (asker routedPermissionAsker) AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question HarnessPermissionQuestion) (acp.RequestPermissionOutcome, AskStatus) {
	if asker.primary.Serves(approvalRequest.Platform, approvalRequest.ConversationID) {
		return asker.primary.AskHarnessPermission(ctx, approvalRequest, question)
	}
	harnessAsker, canAskHarnessPermission := asker.otherwise.(HarnessPermissionAsker)
	if !canAskHarnessPermission {
		return acp.RequestPermissionOutcome{}, AskUnreachable
	}
	return harnessAsker.AskHarnessPermission(ctx, approvalRequest, question)
}
