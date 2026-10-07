package approvalgate

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const TaskEventApprovalWordingFailed = "approval.wording_failed"

func (gate *Gate) UseQuestionWorder(questionWorder holdrecord.QuestionWorder) {
	gate.questionWorder = questionWorder
}

func (gate *Gate) confirmationWording(ctx context.Context, call approvalcore.Call, approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution) string {
	return gate.core.Word(ctx, gate.questionWorder, call, holdrecord.QuestionFacts{
		ResponseLanguage: approvalRequest.ResponseLanguage,
		OriginalRequest:  approvalRequest.Prompt,
		ModelDraft:       approvalRequest.ModelDraft,
		Tool:             toolFacts(approvalRequest),
		Input:            approvalRequest.ToolInput,
		Target:           resolution.Target,
		Choices:          resolution.Choices,
	})
}

func toolFacts(approvalRequest mcpserver.ApprovalRequest) toolcontract.ToolDefinition {
	var tool toolcontract.ToolDefinition
	tool.Name = approvalRequest.ToolName
	tool.ApprovalScope = approvalRequest.ApprovalScope
	tool.ApprovalScopeSummary = approvalRequest.ApprovalScopeSummary
	tool.ApprovalInputFields = approvalRequest.ApprovalInputFields
	tool.SideEffectClass = approvalRequest.SideEffectClass
	return tool
}
