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

func (connectorRuntime *ConnectorRuntime) publishThreadResolution(ctx context.Context, thread *askingThread, optionID string) {
	if thread.interaction == nil {
		return
	}
	adapter, errorValue := connectorRuntime.findAdapter(thread.platform)
	if errorValue != nil {
		return
	}
	resolver, canResolve := adapter.(InteractionResolvingAdapter)
	if !canResolve {
		return
	}
	interactionID := thread.interaction.InteractionID
	messageID := PostedApprovalQuestionMessageID(connectorRuntime.taskRunService.ListTaskEvent(thread.taskRunID))
	if messageID == "" {
		connectorRuntime.logger.Warn("connector."+thread.platform+".interaction.question_message_unknown", slog.String("interactionID", interactionID))
		return
	}
	choiceKey, _ := offeredChoiceKey(thread.interaction, optionID)
	replyTarget := ReplyTarget{ConversationID: thread.conversationID, ReplyTargetID: thread.replyTargetID}
	if errorValue := resolver.ResolveInteraction(context.WithoutCancel(ctx), replyTarget, messageID, interactionID, choiceKey); errorValue != nil {
		connectorRuntime.logger.Warn("connector."+thread.platform+".interaction.resolution_unpublished", slog.String("interactionID", interactionID), slog.String("messageID", messageID), slog.String("error", errorValue.Error()))
	}
}
