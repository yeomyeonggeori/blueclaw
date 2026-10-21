package connectors

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
)

const narrationLineLimit = 6

type narratedCall struct {
	callID  string
	label   string
	outcome string
}

const narrationOutcomeDone = " ✓"
const narrationOutcomeFailed = " ✗"

func (call narratedCall) String() string {
	return call.label + call.outcome
}

func narrationMessage(calls []narratedCall) string {
	if len(calls) == 0 {
		return ""
	}
	shown := calls
	if len(shown) > narrationLineLimit {
		shown = shown[len(shown)-narrationLineLimit:]
	}
	lines := make([]string, 0, len(shown))
	for _, call := range shown {
		lines = append(lines, call.String())
	}
	return "_" + strings.Join(lines, "_\n_") + "_"
}

type turnNarrator struct {
	editor            ReplyEditingAdapter
	deleter           ReplyDeletingAdapter
	adapter           PlatformAdapter
	replyTarget       ReplyTarget
	recordingDelivery func(ReplySender) ReplySender
	logger            *slog.Logger

	mutex      sync.Mutex
	calls      []narratedCall
	messageID  string
	isFinished bool
}

func newTurnNarrator(adapter PlatformAdapter, replyTarget ReplyTarget, recordingDelivery func(ReplySender) ReplySender, logger *slog.Logger) *turnNarrator {
	editor, canEdit := adapter.(ReplyEditingAdapter)
	if !canEdit {
		return nil
	}
	deleter, _ := adapter.(ReplyDeletingAdapter)
	return &turnNarrator{
		editor:            editor,
		deleter:           deleter,
		adapter:           adapter,
		replyTarget:       replyTarget,
		recordingDelivery: recordingDelivery,
		logger:            logger,
	}
}

func (narrator *turnNarrator) toolCallObserver(ctx context.Context) toolcallprogress.Observer {
	if narrator == nil {
		return nil
	}
	return func(update acp.SessionUpdate) {
		narrator.observe(ctx, update)
	}
}

func (narrator *turnNarrator) observe(ctx context.Context, update acp.SessionUpdate) {
	narrator.mutex.Lock()
	defer narrator.mutex.Unlock()
	if narrator.isFinished || !narrator.take(update) {
		return
	}
	message := narrationMessage(narrator.calls)
	if narrator.messageID == "" {
		narrator.startSaying(ctx, message)
		return
	}
	narrator.editor.EditReply(ctx, narrator.replyTarget, narrator.messageID, message)
}

func (narrator *turnNarrator) take(update acp.SessionUpdate) bool {
	if toolCall := update.ToolCall; toolCall != nil {
		narrator.calls = append(narrator.calls, narratedCall{callID: string(toolCall.ToolCallId), label: toolCall.Title})
		return true
	}
	toolCallUpdate := update.ToolCallUpdate
	if toolCallUpdate == nil || toolCallUpdate.Status == nil {
		return false
	}
	outcome, isOutcome := narrationOutcomeOf(*toolCallUpdate.Status)
	if !isOutcome {
		return false
	}
	for index := range narrator.calls {
		if narrator.calls[index].callID != string(toolCallUpdate.ToolCallId) {
			continue
		}
		narrator.calls[index].outcome = outcome
		return true
	}
	return false
}

func narrationOutcomeOf(status acp.ToolCallStatus) (string, bool) {
	switch status {
	case acp.ToolCallStatusCompleted:
		return narrationOutcomeDone, true
	case acp.ToolCallStatusFailed:
		return narrationOutcomeFailed, true
	}
	return "", false
}

func (narrator *turnNarrator) startSaying(ctx context.Context, message string) {
	messageID, errorValue := narrator.adapter.SendReply(ctx, narrator.replyTarget, OutboundReply{Message: message, ReplyKind: ConnectorReplyKindProgress})
	if errorValue != nil {
		return
	}
	narrator.messageID = strings.TrimSpace(messageID)
}

func (narrator *turnNarrator) takeOverSending(sendReply ReplySender) ReplySender {
	if narrator == nil {
		return sendReply
	}
	return func(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
		narrator.mutex.Lock()
		defer narrator.mutex.Unlock()
		if narrator.isFinished {
			return sendReply(ctx, replyTarget, reply)
		}
		if reply.ReplyKind == connectorReplyKindCheckpoint {
			return narrator.sayCheckpoint(ctx, replyTarget, reply)
		}
		return narrator.sayFinal(ctx, replyTarget, reply, sendReply)
	}
}

func (narrator *turnNarrator) sayCheckpoint(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
	if dispatchID, isEdited := narrator.editIntoProgress(ctx, replyTarget, reply); isEdited {
		narrator.beginNextProgress()
		return dispatchID, nil
	}
	dispatchID, errorValue := narrator.recordingDelivery(narrator.adapter.SendReply)(ctx, replyTarget, reply)
	if errorValue != nil {
		return dispatchID, errorValue
	}
	narrator.takeDownProgress(ctx, replyTarget, reply)
	return dispatchID, nil
}

func (narrator *turnNarrator) sayFinal(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply, sendReply ReplySender) (string, error) {
	if narrator.deleter == nil {
		if dispatchID, isEdited := narrator.editIntoProgress(ctx, replyTarget, reply); isEdited {
			narrator.logger.Warn("connector.narration.final_edited_in", "messageID", dispatchID, "taskRunID", reply.TaskRunID, "reason", "the platform cannot delete a message")
			narrator.finish()
			return dispatchID, nil
		}
	}
	dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
	if errorValue != nil {
		return dispatchID, errorValue
	}
	narrator.takeDownProgress(ctx, replyTarget, reply)
	narrator.finish()
	return dispatchID, nil
}

func (narrator *turnNarrator) editIntoProgress(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, bool) {
	if narrator.messageID == "" || !replyIsOnlyWords(reply) {
		return "", false
	}
	dispatchID, errorValue := narrator.recordingDelivery(narrator.editingInto(narrator.messageID))(ctx, replyTarget, reply)
	return dispatchID, errorValue == nil
}

func (narrator *turnNarrator) takeDownProgress(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) {
	messageID := narrator.messageID
	narrator.beginNextProgress()
	if messageID == "" {
		return
	}
	if narrator.deleter == nil {
		narrator.logger.Warn("connector.narration.left_standing", "messageID", messageID, "taskRunID", reply.TaskRunID, "reason", "the reply could not be edited in and the platform cannot delete a message")
		return
	}
	if errorValue := narrator.deleter.DeleteReply(ctx, replyTarget, messageID); errorValue != nil {
		narrator.logger.Warn("connector.narration.left_standing", "messageID", messageID, "taskRunID", reply.TaskRunID, "error", errorValue.Error())
	}
}

func (narrator *turnNarrator) beginNextProgress() {
	narrator.messageID = ""
	narrator.calls = nil
}

func (narrator *turnNarrator) finish() {
	narrator.beginNextProgress()
	narrator.isFinished = true
}

func (narrator *turnNarrator) editingInto(messageID string) ReplySender {
	return func(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
		return messageID, narrator.editor.EditReply(ctx, replyTarget, messageID, reply.Message)
	}
}

func replyIsOnlyWords(reply OutboundReply) bool {
	return strings.TrimSpace(reply.Message) != "" &&
		len(reply.Attachments) == 0 &&
		len(reply.RecoveryActions) == 0 &&
		reply.Interaction == nil
}
