package connectors

import (
	"context"
	"log/slog"
	"strings"
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

func (connectorRuntime *ConnectorRuntime) publishQuestionResolution(ctx context.Context, parked parkedRun, optionKey string) {
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
