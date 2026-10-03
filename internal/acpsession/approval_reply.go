package acpsession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const ApprovalReplyExtensionMethod = "_kim.intern/approvalReply"

type TurnRouter interface {
	Plan(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error)
}

type ApprovalReplyRequest struct {
	SessionID  string `json:"sessionId"`
	ToolCallID string `json:"toolCallId"`
	Reply      string `json:"reply"`
}

type ApprovalReplyResponse struct {
	OptionID string `json:"optionId"`
}

var (
	errNoRouterCanReadTheReply = errors.New("this daemon has no turn router, so a person's answer to an approval cannot be read")
	errReplyCarriesNoWords     = errors.New("an approval reply with no words says nothing to read")
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
	optionID, errorValue := agent.readApprovalReply(ctx, request)
	if errorValue != nil {
		return nil, errorValue
	}
	return ApprovalReplyResponse{OptionID: string(optionID)}, nil
}

func (agent *Agent) readApprovalReply(ctx context.Context, request ApprovalReplyRequest) (acp.PermissionOptionId, error) {
	if agent.turnRouter == nil {
		return "", errNoRouterCanReadTheReply
	}
	reply := strings.TrimSpace(request.Reply)
	if reply == "" {
		return "", errReplyCarriesNoWords
	}
	session, isOpen := agent.session(acp.SessionId(request.SessionID))
	if !isOpen {
		return "", errSessionIsNotOpen
	}
	// The router offers an approval only when it is told which call is waiting,
	// so the held call is looked up here rather than restated by the client.
	waiting, isWaiting := agent.permissionRelay.waitingCall(acp.ToolCallId(request.ToolCallID))
	if !isWaiting {
		return "", errNoCallIsWaitingOnThat
	}
	turnDecision, errorValue := agent.turnRouter.Plan(ctx, approvalReplyRequest(session.context, reply, waiting))
	if errorValue != nil {
		return "", errorValue
	}
	if len(waiting.choices) > 0 {
		return permissionOptionForChoice(turnDecision.Choices, waiting.choices), nil
	}
	return permissionOptionForApprovalSignal(turnDecision.Approval), nil
}

func approvalReplyRequest(sessionContext SessionContext, reply string, waiting waitingCall) agentcontract.AgentRequest {
	request := agentcontract.AgentRequest{
		RequesterPersonID: sessionContext.Requester.PersonID,
		ConversationID:    sessionContext.Addressing.ConversationID,
		Prompt:            reply,
		ResponseLanguage:  sessionContext.Addressing.ResponseLanguage,
	}
	if len(waiting.choices) > 0 {
		request.PendingChoice = agentcontract.PendingChoiceContext{
			TaskRunID:     waiting.approvalRequest.TaskRunID,
			Question:      waiting.confirmation,
			SelectionMode: "single",
			Options:       approvalgate.ChoiceReplyOptions(waiting.choices),
		}
		return request
	}
	request.PendingConfirmation = agentcontract.PendingConfirmationContext{
		TaskRunID: waiting.approvalRequest.TaskRunID,
		Prompt:    waiting.approvalRequest.Prompt,
		Question:  waiting.confirmation,
	}
	return request
}

func permissionOptionForChoice(selected []string, choices []approvalgate.ApprovalChoice) acp.PermissionOptionId {
	for _, selectedKey := range selected {
		if choice, isOffered := approvalgate.ChoiceByKey(choices, selectedKey); isOffered {
			return choiceOptionID(choice.Key)
		}
	}
	return rejectOnceOptionID
}

func permissionOptionForApprovalSignal(approvalSignal *agentcontract.ApprovalSignal) acp.PermissionOptionId {
	if approvalSignal == nil {
		return rejectOnceOptionID
	}
	switch *approvalSignal {
	case agentcontract.ApprovalSignalApprove:
		return approveOnceOptionID
	case agentcontract.ApprovalSignalApproveTask:
		return approveTaskOptionID
	}
	return rejectOnceOptionID
}
