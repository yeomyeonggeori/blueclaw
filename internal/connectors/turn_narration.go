package connectors

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const narrationEventPrefix = "tool."
const narrationRequestedSuffix = ".requested"
const narrationResultSuffix = ".result"
const narrationSubjectLimit = 48
const narrationLineLimit = 6

// A call the agent is making, and how it turned out once it has. A person
// watching three lines needs to know which of them broke.
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

// A tool call reads as the tool's own name and the thing it was pointed at, the
// way a coding agent shows Read(main.go). The name comes from the catalog and
// the argument from the call, so no list of phrases has to be kept beside the
// list of tools.
func narrationOfTurnEvent(rawTurnEvent taskstate.RawTurnEvent) (narratedCall, bool) {
	toolName, isRequest := toolNameOfNarrationEvent(rawTurnEvent.Name, narrationRequestedSuffix)
	if !isRequest {
		return narratedCall{}, false
	}
	label := toolName
	if subject := narrationSubject(rawTurnEvent.Body); subject != "" {
		label = toolName + "(" + subject + ")"
	}
	return narratedCall{callID: narrationCallID(rawTurnEvent.Body), label: label}, true
}

type narratedOutcome struct {
	callID  string
	outcome string
}

func narrationOutcomeOfTurnEvent(rawTurnEvent taskstate.RawTurnEvent) (narratedOutcome, bool) {
	if _, isResult := toolNameOfNarrationEvent(rawTurnEvent.Name, narrationResultSuffix); !isResult {
		return narratedOutcome{}, false
	}
	outcome := narrationOutcomeDone
	if narrationCallFailed(rawTurnEvent.Body) {
		outcome = narrationOutcomeFailed
	}
	return narratedOutcome{callID: narrationCallID(rawTurnEvent.Body), outcome: outcome}, true
}

func toolNameOfNarrationEvent(eventName string, suffix string) (string, bool) {
	if !strings.HasPrefix(eventName, narrationEventPrefix) || !strings.HasSuffix(eventName, suffix) {
		return "", false
	}
	toolName := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(eventName, narrationEventPrefix), suffix))
	return toolName, toolName != ""
}

func narrationCallID(body string) string {
	decoded := struct {
		ObservationID string `json:"observationID"`
	}{}
	json.Unmarshal([]byte(body), &decoded)
	return strings.TrimSpace(decoded.ObservationID)
}

func narrationCallFailed(body string) bool {
	decoded := struct {
		Failure *json.RawMessage `json:"failure"`
	}{}
	return json.Unmarshal([]byte(body), &decoded) == nil && decoded.Failure != nil
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

// turnNarrator keeps one message saying what the agent is doing, and hands it
// over when the answer is ready, so a turn costs the conversation one message
// rather than a silence followed by one.
type turnNarrator struct {
	editor      ReplyEditingAdapter
	adapter     PlatformAdapter
	replyTarget ReplyTarget

	mutex         sync.Mutex
	calls         []narratedCall
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
	message, messageID, hasNews := narrator.record(rawTurnEvent)
	if !hasNews {
		return
	}
	if messageID == "" {
		narrator.startSaying(ctx, message)
		return
	}
	narrator.editor.EditReply(ctx, narrator.replyTarget, messageID, message)
}

func (narrator *turnNarrator) record(rawTurnEvent taskstate.RawTurnEvent) (string, string, bool) {
	narrator.mutex.Lock()
	defer narrator.mutex.Unlock()
	if narrator.isHandedOver || !narrator.take(rawTurnEvent) {
		return "", "", false
	}
	return narrationMessage(narrator.calls), narrator.messageID, true
}

func (narrator *turnNarrator) take(rawTurnEvent taskstate.RawTurnEvent) bool {
	if call, isCall := narrationOfTurnEvent(rawTurnEvent); isCall {
		narrator.calls = append(narrator.calls, call)
		return true
	}
	result, isOutcome := narrationOutcomeOfTurnEvent(rawTurnEvent)
	if !isOutcome || result.callID == "" {
		return false
	}
	for index := range narrator.calls {
		if narrator.calls[index].callID != result.callID {
			continue
		}
		narrator.calls[index].outcome = result.outcome
		return true
	}
	return false
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
