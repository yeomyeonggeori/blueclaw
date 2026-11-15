package connectors

import (
	"context"
	"errors"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

var errNoApprovalReplyReader = errors.New("connector runtime has no approval reply reader configured")

func (connectorRuntime *ConnectorRuntime) UseApprovalReplyReader(approvalReplyReader approvalreply.Reader) {
	connectorRuntime.approvalReplyReader = approvalReplyReader
}

func (connectorRuntime *ConnectorRuntime) readReplyToQuestion(ctx context.Context, taskRunID string, question approvalreply.Question, reply string) (approvalreply.Reading, error) {
	if connectorRuntime.approvalReplyReader == nil {
		return approvalreply.Reading{}, errNoApprovalReplyReader
	}
	observe := func(callRecord agentcontract.LLMCallRecord) {
		connectorRuntime.recordCallsOnTask(taskRunID, []agentcontract.LLMCallRecord{callRecord})
	}
	return connectorRuntime.approvalReplyReader.Read(ctx, question, reply, observe)
}

type answeredOption struct {
	Approval *agentcontract.ApprovalSignal
	Choices  []string
}

func answeredDecision(optionKey string) answeredOption {
	switch optionKey {
	case approvalgate.ApproveOptionKey:
		approval := agentcontract.ApprovalSignalApprove
		return answeredOption{Approval: &approval}
	case approvalgate.RejectOptionKey:
		rejection := agentcontract.ApprovalSignalReject
		return answeredOption{Approval: &rejection}
	default:
		return answeredOption{Choices: []string{optionKey}}
	}
}
