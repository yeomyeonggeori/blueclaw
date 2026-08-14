package connectors

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const narrationEventPrefix = "tool."
const narrationEventSuffix = ".requested"
const narrationSubjectLimit = 48
const narrationLineLimit = 6

// A tool call reads as the tool's own name and the thing it was pointed at, the
// way a coding agent shows Read(main.go). The name comes from the catalog and
// the argument from the call, so no list of phrases has to be kept beside the
// list of tools.
func narrationOfTurnEvent(rawTurnEvent taskstate.RawTurnEvent) string {
	toolName, isRequest := toolNameOfNarrationEvent(rawTurnEvent.Name)
	if !isRequest {
		return ""
	}
	subject := narrationSubject(rawTurnEvent.Body)
	if subject == "" {
		return toolName
	}
	return toolName + "(" + subject + ")"
}

func toolNameOfNarrationEvent(eventName string) (string, bool) {
	if !strings.HasPrefix(eventName, narrationEventPrefix) || !strings.HasSuffix(eventName, narrationEventSuffix) {
		return "", false
	}
	toolName := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(eventName, narrationEventPrefix), narrationEventSuffix))
	return toolName, toolName != ""
}

// Every tool names its subject differently, and a call may carry a dozen fields
// nobody wants to read. The first of these the call actually has is the one
// worth showing.
var narrationSubjectFields = []string{"path", "filePath", "query", "command", "title", "name", "url"}

func narrationSubject(body string) string {
	decoded := struct {
		Input map[string]any `json:"input"`
	}{}
	if json.Unmarshal([]byte(body), &decoded) != nil {
		return ""
	}
	for _, fieldName := range narrationSubjectFields {
		if subject := narrationText(decoded.Input[fieldName]); subject != "" {
			return subject
		}
	}
	return ""
}

func narrationText(value any) string {
	text, isText := value.(string)
	if !isText {
		return ""
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= narrationSubjectLimit {
		return text
	}
	return strings.TrimSpace(text[:narrationSubjectLimit]) + "…"
}

// The lines a person watches are the last few: a turn that called forty tools is
// not something anyone reads from the top.
func narrationMessage(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	shown := lines
	if len(shown) > narrationLineLimit {
		shown = shown[len(shown)-narrationLineLimit:]
	}
	return "_" + strings.Join(shown, "_\n_") + "_"
}

// turnNarrator keeps one message saying what the agent is doing, and hands it
// over when the answer is ready, so a turn costs the conversation one message
// rather than a silence followed by one.
type turnNarrator struct {
	editor      ReplyEditingAdapter
	adapter     PlatformAdapter
	replyTarget ReplyTarget

	mutex         sync.Mutex
	lines         []string
	messageID     string
	isHandedOver  bool
	stopObserving func()
}

func (narrator *turnNarrator) stop() {
	if narrator == nil || narrator.stopObserving == nil {
		return
	}
	narrator.stopObserving()
}

func newTurnNarrator(adapter PlatformAdapter, replyTarget ReplyTarget) *turnNarrator {
	editor, canEdit := adapter.(ReplyEditingAdapter)
	if !canEdit {
		return nil
	}
	return &turnNarrator{editor: editor, adapter: adapter, replyTarget: replyTarget}
}

func (narrator *turnNarrator) observe(ctx context.Context, rawTurnEvent taskstate.RawTurnEvent) {
	if narrator == nil {
		return
	}
	line := narrationOfTurnEvent(rawTurnEvent)
	if line == "" {
		return
	}
	narrator.mutex.Lock()
	if narrator.isHandedOver {
		narrator.mutex.Unlock()
		return
	}
	narrator.lines = append(narrator.lines, line)
	message := narrationMessage(narrator.lines)
	messageID := narrator.messageID
	narrator.mutex.Unlock()

	if messageID == "" {
		narrator.startSaying(ctx, message)
		return
	}
	narrator.editor.EditReply(ctx, narrator.replyTarget, messageID, message)
}

func (narrator *turnNarrator) startSaying(ctx context.Context, message string) {
	messageID, errorValue := narrator.adapter.SendReply(ctx, narrator.replyTarget, OutboundReply{Message: message})
	if errorValue != nil || strings.TrimSpace(messageID) == "" {
		return
	}
	narrator.mutex.Lock()
	if !narrator.isHandedOver {
		narrator.messageID = messageID
	}
	narrator.mutex.Unlock()
}

// The answer belongs where the person has been watching, so the first reply of
// the turn replaces the narration instead of arriving under it. A reply that
// carries more than words is sent whole, since editing cannot add an attachment
// or a question to a message already on screen.
func (narrator *turnNarrator) takeOverSending(
	sendReply func(context.Context, ReplyTarget, OutboundReply) (string, error),
) func(context.Context, ReplyTarget, OutboundReply) (string, error) {
	if narrator == nil {
		return sendReply
	}
	return func(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
		messageID := narrator.claimNarratedMessage()
		if messageID == "" || !replyIsOnlyWords(reply) {
			return sendReply(ctx, replyTarget, reply)
		}
		if errorValue := narrator.editor.EditReply(ctx, replyTarget, messageID, reply.Message); errorValue != nil {
			return sendReply(ctx, replyTarget, reply)
		}
		return messageID, nil
	}
}

func replyIsOnlyWords(reply OutboundReply) bool {
	return strings.TrimSpace(reply.Message) != "" &&
		len(reply.Attachments) == 0 &&
		len(reply.RecoveryActions) == 0 &&
		reply.Interaction == nil
}

// The narrated message is handed over once. Whatever the turn says after that
// is a message of its own.
func (narrator *turnNarrator) claimNarratedMessage() string {
	narrator.mutex.Lock()
	defer narrator.mutex.Unlock()
	messageID := narrator.messageID
	narrator.messageID = ""
	narrator.isHandedOver = true
	return messageID
}
