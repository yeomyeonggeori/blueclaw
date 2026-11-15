package connectors

import (
	"context"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
)

func offeredChoiceKey(interaction *AskInteraction, key string) (string, bool) {
	trimmedKey := strings.TrimSpace(key)
	if interaction == nil || trimmedKey == "" {
		return "", false
	}
	for _, option := range interaction.Options {
		if strings.TrimSpace(option.Key) == trimmedKey {
			return trimmedKey, true
		}
	}
	return "", false
}

type HandoffCloser interface {
	CloseHandoff(ctx context.Context, platform string, conversationID string, handoffID string)
}

func (connectorRuntime *ConnectorRuntime) UseHandoffCloser(handoffCloser HandoffCloser) {
	connectorRuntime.handoffCloser = handoffCloser
}

func leavesTheHandoffUnfinished(optionKey string) bool {
	return optionKey == "" || approvalgate.IsDecliningOption(optionKey)
}

func (connectorRuntime *ConnectorRuntime) closeHandoffLeftUnfinished(ctx context.Context, parked parkedRun, optionKey string) {
	handoffID := strings.TrimSpace(parked.hold.Call.HandoffID)
	if handoffID == "" || connectorRuntime.handoffCloser == nil || !leavesTheHandoffUnfinished(optionKey) {
		return
	}
	connectorRuntime.handoffCloser.CloseHandoff(context.WithoutCancel(ctx), parked.posted.Platform, parked.posted.ConversationID, handoffID)
}

func (connectorRuntime *ConnectorRuntime) publishQuestionResolution(ctx context.Context, parked parkedRun, optionKey string) {
	connectorRuntime.closeHandoffLeftUnfinished(ctx, parked, optionKey)
	interaction := parked.interaction()
	if interaction == nil || parked.posted.HoldID == "" {
		return
	}
	adapter, errorValue := connectorRuntime.findAdapter(parked.posted.Platform)
	if errorValue != nil {
		return
	}
	resolver, canResolve := adapter.(InteractionResolvingAdapter)
	if !canResolve {
		return
	}
	if parked.posted.MessageID == "" {
		connectorRuntime.logger.Warn("connector."+parked.posted.Platform+".interaction.question_message_unknown", slog.String("interactionID", interaction.InteractionID))
		return
	}
	choiceKey, _ := offeredChoiceKey(interaction, optionKey)
	replyTarget := ReplyTarget{ConversationID: parked.posted.ConversationID, ReplyTargetID: parked.posted.ReplyTargetID}
	if errorValue := resolver.ResolveInteraction(context.WithoutCancel(ctx), replyTarget, parked.posted.MessageID, interaction.InteractionID, choiceKey); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+parked.posted.Platform+".interaction.resolution_unpublished", slog.String("interactionID", interaction.InteractionID), slog.String("messageID", parked.posted.MessageID), slog.String("error", errorValue.Error()))
	}
}
