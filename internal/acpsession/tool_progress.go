package acpsession

import (
	"context"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
)

func (agent *Agent) toolCallObserverFor(ctx context.Context, sessionID acp.SessionId, delivery Delivery) toolcallprogress.Observer {
	return func(update acp.SessionUpdate) {
		if errorValue := agent.notifyDelivery(ctx, sessionID, delivery, update); errorValue != nil {
			agent.logger.Warn("acpsession.tool_call.unreported", "sessionID", string(sessionID), "error", errorValue.Error())
		}
	}
}
