package connectors

import (
	"context"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
)

func shouldIgnoreUninvitedAddressing(event PlatformInboundEvent) bool {
	return inboundengagement.ShouldIgnoreUninvitedAddressing(event.Context.ConversationType, event.Context.Addressing.BotMentioned)
}

func isMultiPersonConversation(event PlatformInboundEvent) bool {
	return inboundengagement.IsMultiPersonConversation(event.Context.ConversationType)
}

func (connectorRuntime *ConnectorRuntime) EngagementGate() *inboundengagement.Gate {
	return inboundengagement.NewGate(connectorRuntime.intakeClassifier, connectorRuntime.agentIdentity, connectorRuntime.company, connectorRuntime.logger)
}

func (connectorRuntime *ConnectorRuntime) resolveInboundEngagement(ctx context.Context, platform string, event PlatformInboundEvent) inboundengagement.Decision {
	return connectorRuntime.EngagementGate().Resolve(ctx, platform, inboundengagement.Request{
		Prompt:           event.Prompt,
		MessageID:        event.MessageID,
		ConversationType: event.Context.ConversationType,
		BotMentioned:     event.Context.Addressing.BotMentioned,
		AttachmentsOnly:  event.Context.AttachmentsOnly,
		MessageSentAt:    event.RawReceivedAt,
		SenderName:       event.Context.Sender.Name,
		SenderHandle:     event.Context.Sender.Handle,
		VisibleContext:   event.Context.ToAgentVisibleContext(),
	})
}
