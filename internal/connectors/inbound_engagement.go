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

func (connectorRuntime *ConnectorRuntime) resolveInboundEngagement(ctx context.Context, adapter PlatformAdapter, platform string, event PlatformInboundEvent) inboundengagement.Decision {
	return connectorRuntime.resolveEngagement(ctx, platform, event, func(ctx context.Context) (inboundengagement.Judgment, error) {
		return connectorRuntime.judgeInboundMessage(ctx, adapter, event)
	})
}

func (connectorRuntime *ConnectorRuntime) resolveEngagement(ctx context.Context, platform string, event PlatformInboundEvent, judge func(context.Context) (inboundengagement.Judgment, error)) inboundengagement.Decision {
	return inboundengagement.Resolve(ctx, connectorRuntime.logger, platform, engagementRequestForEvent(event), judge)
}

func engagementRequestForEvent(event PlatformInboundEvent) inboundengagement.Request {
	return inboundengagement.Request{
		MessageID:        event.MessageID,
		ConversationType: event.Context.ConversationType,
		BotMentioned:     event.Context.Addressing.BotMentioned,
		AttachmentsOnly:  event.Context.AttachmentsOnly,
	}
}

func isIgnoredWithoutDeciding(event PlatformInboundEvent) bool {
	return inboundengagement.IsIgnoredWithoutDeciding(engagementRequestForEvent(event))
}
