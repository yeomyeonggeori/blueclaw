package acpsession

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueclaw/internal/mcpserver"
)

var errNoSessionServesTheConversation = errors.New("no acp session is open on the conversation the question belongs to")

type sessionRoute struct {
	sessionID acp.SessionId
	agent     *Agent
}

type SessionRoutes struct {
	mutex  sync.RWMutex
	routes map[string]sessionRoute
	logger *slog.Logger
}

func NewSessionRoutes(logger *slog.Logger) *SessionRoutes {
	return &SessionRoutes{routes: map[string]sessionRoute{}, logger: logger}
}

func (routes *SessionRoutes) hold(sessionContext SessionContext, sessionID acp.SessionId, agent *Agent) {
	routes.mutex.Lock()
	defer routes.mutex.Unlock()
	routes.routes[conversationKey(sessionContext.Addressing.Platform, sessionContext.Addressing.ConversationID)] = sessionRoute{
		sessionID: sessionID,
		agent:     agent,
	}
}

func (routes *SessionRoutes) release(sessionContext SessionContext) {
	routes.mutex.Lock()
	defer routes.mutex.Unlock()
	delete(routes.routes, conversationKey(sessionContext.Addressing.Platform, sessionContext.Addressing.ConversationID))
}

func (routes *SessionRoutes) routeFor(platform string, conversationID string) (sessionRoute, bool) {
	routes.mutex.RLock()
	defer routes.mutex.RUnlock()
	route, isFound := routes.routes[conversationKey(platform, conversationID)]
	return route, isFound
}

func (routes *SessionRoutes) Serves(platform string, conversationID string) bool {
	_, isFound := routes.routeFor(platform, conversationID)
	return isFound
}

func (routes *SessionRoutes) conversationsHeld() []string {
	routes.mutex.RLock()
	defer routes.mutex.RUnlock()
	held := make([]string, 0, len(routes.routes))
	for key := range routes.routes {
		held = append(held, strings.ReplaceAll(key, "\x00", "/"))
	}
	return held
}

func (routes *SessionRoutes) PostQuestion(ctx context.Context, approvalRequest mcpserver.ApprovalRequest, question approvalgate.ParkedQuestion) error {
	route, isFound := routes.routeFor(approvalRequest.Platform, approvalRequest.ConversationID)
	if !isFound {
		routes.logger.Info("acpsession.question.nobody_to_ask",
			"toolName", approvalRequest.ToolName,
			"taskRunID", approvalRequest.TaskRunID,
			"platform", approvalRequest.Platform,
			"conversationID", approvalRequest.ConversationID,
			"conversationsHeld", routes.conversationsHeld(),
		)
		return errNoSessionServesTheConversation
	}
	return route.agent.postParkedQuestion(ctx, route.sessionID, approvalRequest, question)
}
