package connectors

import (
	"context"
	"errors"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

var errNoApprovalReplyReader = errors.New("connector runtime has no approval reply reader configured")

func (connectorRuntime *ConnectorRuntime) UseApprovalReplyReader(approvalReplyReader approvalreply.Reader) {
	connectorRuntime.approvalReplyReader = approvalReplyReader
}

func (connectorRuntime *ConnectorRuntime) readApprovalReply(ctx context.Context, turn *inboundTurn, confirmation pendingApproval) (agentcontract.TurnDecision, bool, error) {
	if connectorRuntime.approvalReplyReader == nil {
		return agentcontract.TurnDecision{}, false, errNoApprovalReplyReader
	}
	question := approvalreply.QuestionFor(confirmation.ApprovalQuestion, confirmation.Choices)
	observe := func(callRecord agentcontract.LLMCallRecord) {
		connectorRuntime.recordIntakeCalls(confirmation.TaskRun.TaskRunID, []agentcontract.LLMCallRecord{callRecord})
	}
	optionID, isAnswer, errorValue := connectorRuntime.approvalReplyReader.Read(ctx, question, turn.event.Prompt, observe)
	if errorValue != nil || !isAnswer {
		return agentcontract.TurnDecision{}, false, errorValue
	}
	return answeredDecision(optionID), true, nil
}

func answeredDecision(optionID string) agentcontract.TurnDecision {
	decision := agentcontract.TurnDecision{Route: agentcontract.TurnRouteContinueTask}
	switch optionID {
	case approvalreply.ApproveOptionID:
		approval := agentcontract.ApprovalSignalApprove
		decision.Approval = &approval
	case approvalreply.RejectOptionID:
		rejection := agentcontract.ApprovalSignalReject
		decision.Approval = &rejection
	default:
		decision.Choices = []string{optionID}
	}
	return decision
}
