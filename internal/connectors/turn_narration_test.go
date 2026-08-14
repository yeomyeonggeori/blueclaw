package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestNarrationNamesTheToolAndWhatItWasPointedAt(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		event    taskstate.RawTurnEvent
		expected string
	}{
		{
			name:     "a path is what a file tool is about",
			event:    taskstate.RawTurnEvent{Name: "tool.file_read.requested", Body: `{"input":{"path":"/workspace/notes.md"}}`},
			expected: "file_read(/workspace/notes.md)",
		},
		{
			name:     "a command is what the terminal is about",
			event:    taskstate.RawTurnEvent{Name: "tool.terminal_run.requested", Body: `{"input":{"command":"bun test"}}`},
			expected: "terminal_run(bun test)",
		},
		{
			name:     "a call with nothing worth showing is still worth naming",
			event:    taskstate.RawTurnEvent{Name: "tool.memory_search.requested", Body: `{"input":{"limit":5}}`},
			expected: "memory_search",
		},
		{
			name:     "a result is not something in progress",
			event:    taskstate.RawTurnEvent{Name: "tool.file_read.result", Body: `{"input":{"path":"/workspace/notes.md"}}`},
			expected: "",
		},
		{
			name:     "an event that is not a tool call says nothing",
			event:    taskstate.RawTurnEvent{Name: "llm.call", Body: `{"input":{"path":"/x"}}`},
			expected: "",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			call, isCall := narrationOfTurnEvent(testCase.event)
			if !isCall {
				call = narratedCall{}
			}
			if call.label != testCase.expected {
				t.Fatalf("narration = %q, want %q", call.label, testCase.expected)
			}
		})
	}
}

func TestNarrationShowsOnlyTheLastLines(t *testing.T) {
	calls := []narratedCall{}
	for _, label := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		calls = append(calls, narratedCall{label: label})
	}
	message := narrationMessage(calls)
	if want := "_c_\n_d_\n_e_\n_f_\n_g_\n_h_"; message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

type recordingNarrationAdapter struct {
	PlatformAdapter
	sentMessages   []string
	editedMessages []string
	sentID         string
}

func (adapter *recordingNarrationAdapter) SendReply(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
	adapter.sentMessages = append(adapter.sentMessages, reply.Message)
	return adapter.sentID, nil
}

func (adapter *recordingNarrationAdapter) EditReply(_ context.Context, _ ReplyTarget, _ string, message string) error {
	adapter.editedMessages = append(adapter.editedMessages, message)
	return nil
}

func TestTheAnswerReplacesTheNarrationRatherThanFollowingIt(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	if narrator == nil {
		t.Fatal("an adapter that can edit should be narrated for")
	}

	narrator.observe(context.Background(), taskstate.RawTurnEvent{Name: "tool.file_read.requested", Body: `{"input":{"path":"/a"}}`})
	narrator.observe(context.Background(), taskstate.RawTurnEvent{Name: "tool.terminal_run.requested", Body: `{"input":{"command":"ls"}}`})

	sent := 0
	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		sent++
		return "message-2", nil
	})
	messageID, errorValue := sendReply(context.Background(), ReplyTarget{}, OutboundReply{Message: "done"})

	if errorValue != nil {
		t.Fatalf("sending the answer failed: %v", errorValue)
	}
	if messageID != "message-1" {
		t.Fatalf("the answer landed in %q, want the narrated message", messageID)
	}
	if sent != 0 {
		t.Fatalf("a second message was sent %d times, want none", sent)
	}
	if len(adapter.sentMessages) != 1 {
		t.Fatalf("messages sent = %d, want one for the narration", len(adapter.sentMessages))
	}
	if last := adapter.editedMessages[len(adapter.editedMessages)-1]; last != "done" {
		t.Fatalf("the narrated message reads %q, want the answer", last)
	}
}

func TestAReplyCarryingMoreThanWordsIsSentWhole(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	narrator.observe(context.Background(), taskstate.RawTurnEvent{Name: "tool.file_read.requested", Body: `{"input":{"path":"/a"}}`})

	sent := 0
	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		sent++
		return "message-2", nil
	})
	_, errorValue := sendReply(context.Background(), ReplyTarget{}, OutboundReply{
		Message:     "here it is",
		Attachments: []toolcontract.FileAttachment{{}},
	})

	if errorValue != nil {
		t.Fatalf("sending the answer failed: %v", errorValue)
	}
	if sent != 1 {
		t.Fatalf("the reply was sent %d times, want once", sent)
	}
}

func TestNarrationStopsOnceTheAnswerHasTakenTheMessage(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	narrator.observe(context.Background(), taskstate.RawTurnEvent{Name: "tool.file_read.requested", Body: `{"input":{"path":"/a"}}`})
	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		return "message-2", nil
	})
	sendReply(context.Background(), ReplyTarget{}, OutboundReply{Message: "done"})

	editsBefore := len(adapter.editedMessages)
	narrator.observe(context.Background(), taskstate.RawTurnEvent{Name: "tool.terminal_run.requested", Body: `{"input":{"command":"ls"}}`})

	if len(adapter.editedMessages) != editsBefore {
		t.Fatalf("the answer was overwritten by a later tool call")
	}
	if len(adapter.sentMessages) != 1 {
		t.Fatalf("a late tool call started a new message")
	}
}

func TestALineSaysHowTheCallTurnedOut(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})

	narrator.observe(context.Background(), taskstate.RawTurnEvent{
		Name: "tool.file_read.requested",
		Body: `{"observationID":"call-1","input":{"path":"/a"}}`,
	})
	narrator.observe(context.Background(), taskstate.RawTurnEvent{
		Name: "tool.terminal_run.requested",
		Body: `{"observationID":"call-2","input":{"command":"ls"}}`,
	})
	narrator.observe(context.Background(), taskstate.RawTurnEvent{
		Name: "tool.file_read.result",
		Body: `{"observationID":"call-1"}`,
	})
	narrator.observe(context.Background(), taskstate.RawTurnEvent{
		Name: "tool.terminal_run.result",
		Body: `{"observationID":"call-2","failure":{"reason":"exit 1"}}`,
	})

	last := adapter.editedMessages[len(adapter.editedMessages)-1]
	if want := "_file_read(/a) ✓_\n_terminal_run(ls) ✗_"; last != want {
		t.Fatalf("narration reads %q, want %q", last, want)
	}
}

func TestAResultForACallNobodyNarratedChangesNothing(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})

	narrator.observe(context.Background(), taskstate.RawTurnEvent{
		Name: "tool.file_read.result",
		Body: `{"observationID":"call-9"}`,
	})

	if len(adapter.sentMessages) != 0 || len(adapter.editedMessages) != 0 {
		t.Fatal("a result on its own started a narration")
	}
}
