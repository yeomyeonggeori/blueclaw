package connectors

import (
	"context"
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
	editor      ReplyEditingAdapter
	deleter     ReplyDeletingAdapter
	adapter     PlatformAdapter
	replyTarget ReplyTarget

	mutex        sync.Mutex
	calls        []narratedCall
	messageID    string
	isHandedOver bool
}

func newTurnNarrator(adapter PlatformAdapter, replyTarget ReplyTarget) *turnNarrator {
	editor, canEdit := adapter.(ReplyEditingAdapter)
	if !canEdit {
		return nil
	}
	deleter, _ := adapter.(ReplyDeletingAdapter)
	return &turnNarrator{editor: editor, deleter: deleter, adapter: adapter, replyTarget: replyTarget}
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
	message, messageID, hasNews := narrator.record(update)
	if !hasNews {
		return
	}
	if messageID == "" {
		narrator.startSaying(ctx, message)
		return
	}
	narrator.editor.EditReply(ctx, narrator.replyTarget, messageID, message)
}

func (narrator *turnNarrator) record(update acp.SessionUpdate) (string, string, bool) {
	narrator.mutex.Lock()
	defer narrator.mutex.Unlock()
	if narrator.isHandedOver || !narrator.take(update) {
		return "", "", false
	}
	return narrationMessage(narrator.calls), narrator.messageID, true
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
	if errorValue != nil || strings.TrimSpace(messageID) == "" {
		return
	}
	narrator.mutex.Lock()
	if !narrator.isHandedOver {
		narrator.messageID = messageID
	}
	narrator.mutex.Unlock()
}

func (narrator *turnNarrator) takeOverSending(sendReply ReplySender, recordingDelivery func(ReplySender) ReplySender) ReplySender {
	if narrator == nil {
		return sendReply
	}
	return func(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
		messageID := narrator.claimNarratedMessage()
		if messageID == "" {
			return sendReply(ctx, replyTarget, reply)
		}
		if narrator.deleter == nil {
			if !replyIsOnlyWords(reply) {
				return sendReply(ctx, replyTarget, reply)
			}
			if dispatchID, errorValue := recordingDelivery(narrator.editingInto(messageID))(ctx, replyTarget, reply); errorValue == nil {
				return dispatchID, nil
			}
			return sendReply(ctx, replyTarget, reply)
		}
		dispatchID, errorValue := sendReply(ctx, replyTarget, reply)
		if errorValue != nil {
			return dispatchID, errorValue
		}
		narrator.deleter.DeleteReply(ctx, replyTarget, messageID)
		return dispatchID, nil
	}
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

func (narrator *turnNarrator) claimNarratedMessage() string {
	narrator.mutex.Lock()
	defer narrator.mutex.Unlock()
	messageID := narrator.messageID
	narrator.messageID = ""
	narrator.isHandedOver = true
	return messageID
}
