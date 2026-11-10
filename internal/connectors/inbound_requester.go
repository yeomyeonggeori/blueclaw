package connectors

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func (connectorRuntime *ConnectorRuntime) withInboundRequester(ctx context.Context, turn *inboundTurn) context.Context {
	return model.ContextWithRequestContext(ctx, model.RequestContext{
		RequesterPersonID:       turn.personID,
		RequesterEmail:          connectorRuntime.requesterEmailForEvent(turn.personID, turn.event),
		RequesterPlatformUserID: turn.event.SenderID,
		Platform:                turn.platform,
	})
}
