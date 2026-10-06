package connectors

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (connectorRuntime *ConnectorRuntime) recordingDelivery(deliver ReplySender) ReplySender {
	return func(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
		dispatchID, errorValue := deliver(ctx, replyTarget, reply)
		if errorValue != nil {
			return dispatchID, errorValue
		}
		event, _ := connectorEventFromContext(ctx)
		connectorRuntime.recordReplySent(event, replyTarget, reply, dispatchID)
		return dispatchID, nil
	}
}

func (connectorRuntime *ConnectorRuntime) recordReplySent(event PlatformInboundEvent, replyTarget ReplyTarget, reply OutboundReply, dispatchID string) {
	connectorRuntime.sentAttachmentSources.RecordReply(event.Platform, dispatchID, reply.Attachments)
	connectorRuntime.appendConnectorReplyEvent(reply.TaskRunID, agentcontract.TaskEventConnectorReplySent, connectorReplyEventBody(event, reply, reply.OutboxID, dispatchID, ""))
	connectorRuntime.recordTaskWaitTokenForReply(event.Platform, event, replyTarget, reply, dispatchID)
}
