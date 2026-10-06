package connectors

import (
	"context"
	"errors"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	ApproveOptionID = "approve"
	RejectOptionID  = "reject"
)

var errNoApprovalReplyReader = errors.New("connector runtime has no approval reply reader configured")

func (connectorRuntime *ConnectorRuntime) UseApprovalReplyReader(approvalReplyReader approvalreply.Reader) {
	connectorRuntime.approvalReplyReader = approvalReplyReader
}

func (connectorRuntime *ConnectorRuntime) readReplyToQuestion(ctx context.Context, taskRunID string, question approvalreply.Question, reply string) (string, bool, error) {
	if connectorRuntime.approvalReplyReader == nil {
		return "", false, errNoApprovalReplyReader
	}
	observe := func(callRecord agentcontract.LLMCallRecord) {
		connectorRuntime.recordCallsOnTask(taskRunID, []agentcontract.LLMCallRecord{callRecord})
	}
	return connectorRuntime.approvalReplyReader.Read(ctx, question, reply, observe)
}

func approvalQuestionFor(text string, choices []holdrecord.Choice) approvalreply.Question {
	return approvalreply.Question{Text: text, Options: approvalreply.OptionsOf(offersOf(choices))}
}

func offersOf(choices []holdrecord.Choice) []approvalreply.Offer {
	if len(choices) == 0 {
		return []approvalreply.Offer{
			{ID: ApproveOptionID},
			{ID: RejectOptionID, IsDeclining: true},
		}
	}
	offers := []approvalreply.Offer{}
	for _, replyOption := range approvalgate.ChoiceReplyOptions(choices) {
		offers = append(offers, approvalreply.Offer{ID: replyOption.Key, Name: replyOption.Label, IsDeclining: replyOption.Key == approvalgate.CancelChoiceKey})
	}
	return offers
}

func answeredDecision(optionID string) agentcontract.TurnDecision {
	decision := agentcontract.TurnDecision{Route: agentcontract.TurnRouteContinueTask}
	switch optionID {
	case ApproveOptionID:
		approval := agentcontract.ApprovalSignalApprove
		decision.Approval = &approval
	case RejectOptionID:
		rejection := agentcontract.ApprovalSignalReject
		decision.Approval = &rejection
	default:
		decision.Choices = []string{optionID}
	}
	return decision
}
