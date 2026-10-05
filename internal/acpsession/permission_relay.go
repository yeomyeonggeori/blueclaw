package acpsession

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalrecord"
	"log/slog"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	approveOnceOptionID  = acp.PermissionOptionId("approve_once")
	rejectOnceOptionID   = acp.PermissionOptionId("reject_once")
	chooseOptionIDPrefix = "choose:"
)

type permissionRoute struct {
	sessionID acp.SessionId
	agent     *Agent
}

type waitingCall struct {
	approvalRequest mcpserver.ApprovalRequest
	confirmation    string
	options         []acp.PermissionOption
}

type PermissionRelay struct {
	mutex   sync.RWMutex
	routes  map[string]permissionRoute
	waiting map[acp.ToolCallId]waitingCall
	logger  *slog.Logger
}

func NewPermissionRelay(logger *slog.Logger) *PermissionRelay {
	return &PermissionRelay{
		routes:  map[string]permissionRoute{},
		waiting: map[acp.ToolCallId]waitingCall{},
		logger:  logger,
	}
}

func (relay *PermissionRelay) holdWaitingCall(toolCallID acp.ToolCallId, call waitingCall) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.waiting[toolCallID] = call
}

func (relay *PermissionRelay) releaseWaitingCall(toolCallID acp.ToolCallId) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	delete(relay.waiting, toolCallID)
}

func (relay *PermissionRelay) waitingCall(toolCallID acp.ToolCallId) (waitingCall, bool) {
	relay.mutex.RLock()
	defer relay.mutex.RUnlock()
	call, isWaiting := relay.waiting[toolCallID]
	return call, isWaiting
}

func (relay *PermissionRelay) hold(sessionContext SessionContext, sessionID acp.SessionId, agent *Agent) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.routes[conversationKey(sessionContext.Addressing.Platform, sessionContext.Addressing.ConversationID)] = permissionRoute{
		sessionID: sessionID,
		agent:     agent,
	}
}

func (relay *PermissionRelay) release(sessionContext SessionContext) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	delete(relay.routes, conversationKey(sessionContext.Addressing.Platform, sessionContext.Addressing.ConversationID))
}

func (relay *PermissionRelay) routeFor(platform string, conversationID string) (permissionRoute, bool) {
	relay.mutex.RLock()
	defer relay.mutex.RUnlock()
	route, isFound := relay.routes[conversationKey(platform, conversationID)]
	return route, isFound
}

func (relay *PermissionRelay) conversationsHeld() []string {
	relay.mutex.RLock()
	defer relay.mutex.RUnlock()
	held := make([]string, 0, len(relay.routes))
	for key := range relay.routes {
		held = append(held, strings.ReplaceAll(key, "\x00", "/"))
	}
	return held
}

func (relay *PermissionRelay) AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.PermissionQuestion) (approvalgate.ApprovalAnswer, bool) {
	outcome, isAnswered := relay.askOutcome(ctx, approvalRequest, question.Confirmation, permissionToolCall(approvalRequest, question.Confirmation), permissionOptions(question.Choices))
	if !isAnswered {
		return approvalgate.ApprovalAnswer{}, false
	}
	return approvalAnswerForOutcome(outcome)
}

func (relay *PermissionRelay) AskHarnessPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.HarnessPermissionQuestion) (acp.RequestPermissionOutcome, bool) {
	outcome, isAnswered := relay.askOutcome(ctx, approvalRequest, question.Text, question.ToolCall, question.Options)
	if !isAnswered || !selectsOneOf(outcome, question.Options) {
		return acp.RequestPermissionOutcome{}, false
	}
	return outcome, true
}

func selectsOneOf(outcome acp.RequestPermissionOutcome, options []acp.PermissionOption) bool {
	if outcome.Selected == nil {
		return false
	}
	for _, permissionOption := range options {
		if permissionOption.OptionId == outcome.Selected.OptionId {
			return true
		}
	}
	return false
}

func (relay *PermissionRelay) askOutcome(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, confirmation string, toolCall acp.ToolCallUpdate, options []acp.PermissionOption) (acp.RequestPermissionOutcome, bool) {
	route, isFound := relay.routeFor(approvalRequest.Platform, approvalRequest.ConversationID)
	if !isFound {
		relay.logger.Info("acpsession.permission.nobody_to_ask",
			"toolName", approvalRequest.ToolName,
			"taskRunID", approvalRequest.TaskRunID,
			"platform", approvalRequest.Platform,
			"conversationID", approvalRequest.ConversationID,
			"conversationsHeld", relay.conversationsHeld(),
		)
		return acp.RequestPermissionOutcome{}, false
	}
	relay.holdWaitingCall(toolCall.ToolCallId, waitingCall{approvalRequest: approvalRequest, confirmation: confirmation, options: options})
	defer relay.releaseWaitingCall(toolCall.ToolCallId)
	response, errorValue := route.agent.askThePerson(ctx, approvalRequest, confirmation, acp.RequestPermissionRequest{
		SessionId: route.sessionID,
		ToolCall:  toolCall,
		Options:   options,
	})
	if errorValue != nil {
		relay.logger.Warn("acpsession.permission.unanswered", "toolName", approvalRequest.ToolName, "taskRunID", approvalRequest.TaskRunID, "error", errorValue.Error())
		return acp.RequestPermissionOutcome{}, false
	}
	return response.Outcome, true
}

func permissionToolCall(approvalRequest mcpserver.ApprovalRequest, confirmation string) acp.ToolCallUpdate {
	title := confirmation
	toolCall := acp.ToolCallUpdate{
		ToolCallId: acp.ToolCallId(approvalgate.HeldCallID(approvalRequest.ToolName, approvalRequest.ToolInput)),
		Title:      &title,
	}
	rawInput := map[string]any{}
	if json.Unmarshal(approvalRequest.ToolInput, &rawInput) == nil {
		toolCall.RawInput = rawInput
	}
	return toolCall
}

func permissionOptions(choices []approvalrecord.Choice) []acp.PermissionOption {
	if len(choices) > 0 {
		return choicePermissionOptions(choices)
	}
	return []acp.PermissionOption{{
		OptionId: approveOnceOptionID,
		Kind:     acp.PermissionOptionKindAllowOnce,
		Name:     "approve this call",
	}, rejectOption()}
}

func choicePermissionOptions(choices []approvalrecord.Choice) []acp.PermissionOption {
	options := []acp.PermissionOption{}
	for _, choice := range choices {
		options = append(options, acp.PermissionOption{
			OptionId: choiceOptionID(choice.Key),
			Kind:     acp.PermissionOptionKindAllowOnce,
			Name:     choiceOptionName(choice),
		})
	}
	return append(options, rejectOption())
}

func choiceOptionID(choiceKey string) acp.PermissionOptionId {
	return acp.PermissionOptionId(chooseOptionIDPrefix + strings.TrimSpace(choiceKey))
}

func choiceOptionName(choice approvalrecord.Choice) string {
	if choice.DefersTheCall() {
		return "approve this call to run at " + strings.TrimSpace(choice.StartsAt)
	}
	return "approve this call to run now"
}

func rejectOption() acp.PermissionOption {
	return acp.PermissionOption{
		OptionId: rejectOnceOptionID,
		Kind:     acp.PermissionOptionKindRejectOnce,
		Name:     "decline this call",
	}
}

func approvalAnswerForOutcome(outcome acp.RequestPermissionOutcome) (approvalgate.ApprovalAnswer, bool) {
	if outcome.Selected == nil {
		return approvalgate.ApprovalAnswer{}, false
	}
	optionID := string(outcome.Selected.OptionId)
	if choiceKey, isChoice := strings.CutPrefix(optionID, chooseOptionIDPrefix); isChoice {
		return approvalgate.ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove, ChoiceKey: choiceKey}, true
	}
	switch outcome.Selected.OptionId {
	case approveOnceOptionID:
		return approvalgate.ApprovalAnswer{Signal: agentcontract.ApprovalSignalApprove}, true
	case rejectOnceOptionID:
		return approvalgate.ApprovalAnswer{Signal: agentcontract.ApprovalSignalReject}, true
	}
	return approvalgate.ApprovalAnswer{}, false
}
