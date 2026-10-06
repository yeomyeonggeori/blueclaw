package connectors

import (
	"context"
	"errors"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	ApproveOptionID = "approve"
	RejectOptionID  = "reject"

	approveOptionName = "approve this call as asked"
)

var errNoApprovalReplyReader = errors.New("connector runtime has no approval reply reader configured")

func (connectorRuntime *ConnectorRuntime) UseApprovalReplyReader(approvalReplyReader approvalreply.Reader) {
	connectorRuntime.approvalReplyReader = approvalReplyReader
}

func (connectorRuntime *ConnectorRuntime) readApprovalReply(ctx context.Context, turn *inboundTurn, confirmation pendingApproval) (agentcontract.TurnDecision, bool, error) {
	if connectorRuntime.approvalReplyReader == nil {
		return agentcontract.TurnDecision{}, false, errNoApprovalReplyReader
	}
	question := approvalQuestionFor(confirmation.ApprovalQuestion, confirmation.Choices)
	observe := func(callRecord agentcontract.LLMCallRecord) {
		connectorRuntime.recordIntakeCalls(confirmation.TaskRun.TaskRunID, []agentcontract.LLMCallRecord{callRecord})
	}
	optionID, isAnswer, errorValue := connectorRuntime.approvalReplyReader.Read(ctx, question, turn.event.Prompt, observe)
	if errorValue != nil || !isAnswer {
		return agentcontract.TurnDecision{}, false, errorValue
	}
	return answeredDecision(optionID), true, nil
}

func approvalQuestionFor(text string, choices []holdrecord.Choice) approvalreply.Question {
	if len(choices) == 0 {
		return approvalreply.Question{Text: text, Options: []approvalreply.Option{
			{ID: ApproveOptionID, Meaning: approvalreply.AllowMeaning(approveOptionName)},
			{ID: RejectOptionID, Meaning: approvalreply.RejectMeaning},
		}}
	}
	options := []approvalreply.Option{}
	for _, replyOption := range approvalrecord.ChoiceReplyOptions(choices) {
		options = append(options, approvalreply.Option{ID: replyOption.Key, Meaning: choiceMeaning(replyOption)})
	}
	return approvalreply.Question{Text: text, Options: options}
}

func choiceMeaning(replyOption agentcontract.ChoiceReplyOption) string {
	if replyOption.Key == approvalrecord.CancelChoiceKey {
		return approvalreply.RejectMeaning
	}
	return approvalreply.AllowMeaning(replyOption.Label)
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
