package acpsession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalreply"
	"github.com/yeomyeonggeori/blueclaw/internal/connectors"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const ApprovalReplyExtensionMethod = "_kim.intern/approvalReply"

type ApprovalReplyRequest struct {
	SessionID     string `json:"sessionId"`
	ToolCallID    string `json:"toolCallId"`
	Reply         string `json:"reply"`
	MessageID     string `json:"messageId"`
	ReplyTargetID string `json:"replyTargetId"`
	IsThread      bool   `json:"isThread"`
}

type ApprovalReplyResponse struct {
	IsAnswer bool   `json:"isAnswer"`
	OptionID string `json:"optionId,omitempty"`
}

var (
	errNoReaderCanReadTheReply = errors.New("this daemon has no approval reply reader, so a person's answer to an approval cannot be read")
	errNoCallIsWaitingOnThat   = errors.New("no held call by that tool call id is waiting for an answer")
)

func (agent *Agent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case ApprovalReplyExtensionMethod:
		return agent.answerApprovalReply(ctx, params)
	case DeliveredExtensionMethod:
		return agent.settleDelivered(params)
	case UndeliveredExtensionMethod:
		return agent.settleUndelivered(params)
	}
	return nil, acp.NewMethodNotFound(method)
}

func (agent *Agent) answerApprovalReply(ctx context.Context, params json.RawMessage) (any, error) {
	request := ApprovalReplyRequest{}
	if errorValue := json.Unmarshal(params, &request); errorValue != nil {
		return nil, errorValue
	}
	return agent.readApprovalReply(ctx, request)
}

func (agent *Agent) readApprovalReply(ctx context.Context, request ApprovalReplyRequest) (ApprovalReplyResponse, error) {
	if agent.replyReader == nil {
		return ApprovalReplyResponse{}, errNoReaderCanReadTheReply
	}
	session, isOpen := agent.session(acp.SessionId(request.SessionID))
	if !isOpen {
		return ApprovalReplyResponse{}, errSessionIsNotOpen
	}
	waiting, isWaiting := agent.permissionRelay.waitingCall(acp.ToolCallId(request.ToolCallID))
	if !isWaiting {
		return ApprovalReplyResponse{}, errNoCallIsWaitingOnThat
	}
	if !agent.postedQuestionOf(session.context, waiting).IsAnsweredBy(replyPlacementOf(session.context, request)) {
		return ApprovalReplyResponse{}, nil
	}
	question := approvalreply.Question{Text: waiting.confirmation, Options: approvalgate.ReplyOptionsOf(waiting.options)}
	optionID, isAnswer, errorValue := agent.replyReader.Read(ctx, question, request.Reply, agent.ledgerObserver(waiting.approvalRequest.TaskRunID))
	if errorValue != nil {
		return ApprovalReplyResponse{}, errorValue
	}
	if !isAnswer {
		return ApprovalReplyResponse{}, nil
	}
	return ApprovalReplyResponse{IsAnswer: true, OptionID: optionID}, nil
}

func (agent *Agent) postedQuestionOf(sessionContext SessionContext, waiting waitingCall) connectors.PostedQuestion {
	return connectors.PostedQuestion{
		ConversationID: sessionContext.Addressing.ConversationID,
		ReplyTargetID:  waiting.approvalRequest.ReplyTargetID,
		MessageID:      agent.postedQuestionMessageID(waiting.approvalRequest.TaskRunID),
	}
}

func (agent *Agent) postedQuestionMessageID(taskRunID string) string {
	if agent.taskRunStore == nil {
		return ""
	}
	return connectors.PostedApprovalQuestionMessageID(agent.taskRunStore.ListTaskEvent(taskRunID))
}

func replyPlacementOf(sessionContext SessionContext, request ApprovalReplyRequest) connectors.MessagePlacement {
	return connectors.MessagePlacement{
		ConversationID: sessionContext.Addressing.ConversationID,
		ReplyTargetID:  strings.TrimSpace(request.ReplyTargetID),
		IsThread:       request.IsThread,
	}
}

func (agent *Agent) ledgerObserver(taskRunID string) agentcontract.LLMCallObserver {
	return func(callRecord agentcontract.LLMCallRecord) {
		if agent.taskRunStore != nil {
			agent.taskRunStore.AppendLLMCall(taskRunID, callRecord)
		}
	}
}
