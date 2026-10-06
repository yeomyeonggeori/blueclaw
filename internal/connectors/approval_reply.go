package connectors

import (
	"context"
	"errors"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
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

type answeredOption struct {
	Approval *agentcontract.ApprovalSignal
	Choices  []string
}

func answeredDecision(optionID string) answeredOption {
	switch optionID {
	case ApproveOptionID:
		approval := agentcontract.ApprovalSignalApprove
		return answeredOption{Approval: &approval}
	case RejectOptionID:
		rejection := agentcontract.ApprovalSignalReject
		return answeredOption{Approval: &rejection}
	default:
		return answeredOption{Choices: []string{optionID}}
	}
}
