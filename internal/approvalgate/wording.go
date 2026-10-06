package approvalgate

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const TaskEventApprovalWordingFailed = "approval.wording_failed"

var errNoQuestionWorder = errors.New("approval wording needs a question worder and none is configured")

func (gate *Gate) UseQuestionWorder(questionWorder holdrecord.QuestionWorder) {
	gate.questionWorder = questionWorder
}

func (gate *Gate) confirmationWording(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, resolution ApprovalTargetResolution) string {
	if gate.questionWorder == nil {
		gate.recordWordingFailure(approvalRequest, errNoQuestionWorder)
		return strings.TrimSpace(approvalRequest.ToolName)
	}
	wording := gate.questionWorder.WordQuestion(ctx, holdrecord.QuestionFacts{
		ResponseLanguage: approvalRequest.ResponseLanguage,
		OriginalRequest:  approvalRequest.Prompt,
		ModelDraft:       approvalRequest.ModelDraft,
		Tool:             toolFacts(approvalRequest),
		Input:            approvalRequest.ToolInput,
		Target:           resolution.Target,
		Choices:          resolution.Choices,
	})
	if wording.Failure != nil {
		gate.recordWordingFailure(approvalRequest, wording.Failure)
	}
	return wording.Text
}

func (gate *Gate) recordWordingFailure(approvalRequest mcpserver.ApprovalRequest, errorValue error) {
	taskRunID := strings.TrimSpace(approvalRequest.TaskRunID)
	toolName := strings.TrimSpace(approvalRequest.ToolName)
	slog.Warn("approvalgate.wording_failed", "taskRunID", taskRunID, "toolName", toolName, "error", errorValue.Error())
	if taskRunID == "" {
		return
	}
	gate.taskRunService.AppendTaskEvent(taskRunID, TaskEventApprovalWordingFailed, marshalEventBody(map[string]string{
		"toolName": toolName,
		"error":    errorValue.Error(),
	}))
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
