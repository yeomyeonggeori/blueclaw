package connectors

import (
	"context"
	"log/slog"
	"strings"
)

const engagedAckReactionEmojiName = "eyes"

func (connectorRuntime *ConnectorRuntime) applyEngagedAckReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, isEngaged bool) string {
	if !isEngaged || !isMultiPersonConversation(event) {
		return ""
	}
	reactionAdapter, isSupported := adapter.(MessageReactionAdapter)
	if !isSupported {
		return ""
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      engagedAckReactionEmojiName,
		Reason:         "engaged_ack",
	}
	if errorValue := reactionAdapter.AddReaction(ctx, target); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".reaction.failed", slog.String("messageID", event.MessageID), slog.String("emojiName", target.EmojiName), slog.String("error", errorValue.Error()))
		return ""
	}
	return engagedAckReactionEmojiName
}

func (connectorRuntime *ConnectorRuntime) clearEngagedAckReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, emojiName string) {
	if strings.TrimSpace(emojiName) == "" {
		return
	}
	removalAdapter, isSupported := adapter.(MessageReactionRemovalAdapter)
	if !isSupported {
		return
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      emojiName,
		Reason:         "engaged_ack_cleared",
	}
	if errorValue := removalAdapter.RemoveReaction(ctx, target); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".reaction.remove_failed", slog.String("messageID", event.MessageID), slog.String("emojiName", emojiName), slog.String("error", errorValue.Error()))
	}
}

func (connectorRuntime *ConnectorRuntime) addAddressingReaction(ctx context.Context, platform string, adapter PlatformAdapter, event PlatformInboundEvent, reactionEmojiName string) {
	reactionAdapter, isSupported := adapter.(MessageReactionAdapter)
	if !isSupported {
		return
	}
	target := ReactionTarget{
		Platform:       platform,
		ConversationID: event.ConversationID,
		MessageID:      event.MessageID,
		EmojiName:      reactionEmojiName,
		Reason:         "addressing_ack",
	}
	if errorValue := reactionAdapter.AddReaction(ctx, target); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+platform+".reaction.failed", slog.String("messageID", event.MessageID), slog.String("emojiName", target.EmojiName), slog.String("error", errorValue.Error()))
	}
}
