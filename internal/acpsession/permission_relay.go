package acpsession

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const (
	approveOnceOptionID = acp.PermissionOptionId("approve_once")
	approveTaskOptionID = acp.PermissionOptionId("approve_task")
	rejectOnceOptionID  = acp.PermissionOptionId("reject_once")
)

type permissionRoute struct {
	sessionID  acp.SessionId
	connection *acp.AgentSideConnection
}

type waitingCall struct {
	approvalRequest mcpserver.ApprovalRequest
	confirmation    string
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

func (relay *PermissionRelay) hold(sessionContext SessionContext, sessionID acp.SessionId, connection *acp.AgentSideConnection) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.routes[conversationKey(sessionContext.Addressing.Platform, sessionContext.Addressing.ConversationID)] = permissionRoute{
		sessionID:  sessionID,
		connection: connection,
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

func (relay *PermissionRelay) AskPermission(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, confirmation string) (agentcontract.ApprovalSignal, bool) {
	route, isFound := relay.routeFor(approvalRequest.Platform, approvalRequest.ConversationID)
	if !isFound {
		relay.logger.Info("acpsession.permission.nobody_to_ask",
			"toolName", approvalRequest.ToolName,
			"taskRunID", approvalRequest.TaskRunID,
			"platform", approvalRequest.Platform,
			"conversationID", approvalRequest.ConversationID,
			"conversationsHeld", relay.conversationsHeld(),
		)
		return "", false
	}
	toolCall := permissionToolCall(approvalRequest, confirmation)
	relay.holdWaitingCall(toolCall.ToolCallId, waitingCall{approvalRequest: approvalRequest, confirmation: confirmation})
	defer relay.releaseWaitingCall(toolCall.ToolCallId)
	response, errorValue := route.connection.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: route.sessionID,
		ToolCall:  toolCall,
		Options:   permissionOptions(approvalRequest),
	})
	if errorValue != nil {
		relay.logger.Warn("acpsession.permission.unanswered", "toolName", approvalRequest.ToolName, "taskRunID", approvalRequest.TaskRunID, "error", errorValue.Error())
		return "", false
	}
	return approvalSignalForOutcome(response.Outcome)
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

func permissionOptions(approvalRequest mcpserver.ApprovalRequest) []acp.PermissionOption {
	options := []acp.PermissionOption{{
		OptionId: approveOnceOptionID,
		Kind:     acp.PermissionOptionKindAllowOnce,
		Name:     "approve this call",
	}}
	if approvalRequest.ApprovalScope != "" {
		options = append(options, acp.PermissionOption{
			OptionId: approveTaskOptionID,
			Kind:     acp.PermissionOptionKindAllowAlways,
			Name:     "approve this call and the rest of this task",
		})
	}
	return append(options, acp.PermissionOption{
		OptionId: rejectOnceOptionID,
		Kind:     acp.PermissionOptionKindRejectOnce,
		Name:     "decline this call",
	})
}

func approvalSignalForOutcome(outcome acp.RequestPermissionOutcome) (agentcontract.ApprovalSignal, bool) {
	if outcome.Selected == nil {
		return "", false
	}
	switch outcome.Selected.OptionId {
	case approveOnceOptionID:
		return agentcontract.ApprovalSignalApprove, true
	case approveTaskOptionID:
		return agentcontract.ApprovalSignalApproveTask, true
	case rejectOnceOptionID:
		return agentcontract.ApprovalSignalReject, true
	}
	return "", false
}
