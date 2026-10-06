package connectors

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

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

type deletingNarrationAdapter struct {
	recordingNarrationAdapter
	deletedMessages []string
}

func (adapter *deletingNarrationAdapter) DeleteReply(_ context.Context, _ ReplyTarget, messageID string) error {
	adapter.deletedMessages = append(adapter.deletedMessages, messageID)
	return nil
}

// The answer must be a message every reader shows, not an overlay some readers
// miss: a person's client kept reading "message_context" while the failure
// notice lived only in an edit it never applied.
func TestTheAnswerArrivesWholeAndTheNarrationComesDown(t *testing.T) {
	adapter := &deletingNarrationAdapter{recordingNarrationAdapter: recordingNarrationAdapter{sentID: "narration-1"}}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	narrator.observe(context.Background(), acp.StartToolCall("call-a", "file_read(/a)"))

	sent := 0
	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		sent++
		return "answer-1", nil
	}, deliveredUnrecorded)
	messageID, errorValue := sendReply(context.Background(), ReplyTarget{}, OutboundReply{Message: "done"})

	if errorValue != nil {
		t.Fatalf("sending the answer failed: %v", errorValue)
	}
	if messageID != "answer-1" || sent != 1 {
		t.Fatalf("the answer landed in %q after %d sends, want its own message", messageID, sent)
	}
	if len(adapter.deletedMessages) != 1 || adapter.deletedMessages[0] != "narration-1" {
		t.Fatalf("narration deletions = %v, want the narrated message taken down", adapter.deletedMessages)
	}
}

func TestAFailedAnswerLeavesTheNarrationStanding(t *testing.T) {
	adapter := &deletingNarrationAdapter{recordingNarrationAdapter: recordingNarrationAdapter{sentID: "narration-1"}}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	narrator.observe(context.Background(), acp.StartToolCall("call-a", "file_read(/a)"))

	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		return "", context.Canceled
	}, deliveredUnrecorded)
	sendReply(context.Background(), ReplyTarget{}, OutboundReply{Message: "done"})

	if len(adapter.deletedMessages) != 0 {
		t.Fatal("an undelivered answer must not take the narration down with it")
	}
}

func TestTheAnswerReplacesTheNarrationRatherThanFollowingIt(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	if narrator == nil {
		t.Fatal("an adapter that can edit should be narrated for")
	}

	narrator.observe(context.Background(), acp.StartToolCall("call-a", "file_read(/a)"))
	narrator.observe(context.Background(), acp.StartToolCall("call-b", "bash(ls)"))

	sent := 0
	recordedDeliveries := []string{}
	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		sent++
		return "message-2", nil
	}, recordingDeliveriesInto(&recordedDeliveries))
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
	if len(recordedDeliveries) != 1 || recordedDeliveries[0] != "message-1" {
		t.Fatalf("the answer edited into the narration was recorded as %v, want the narrated message once", recordedDeliveries)
	}
}

func deliveredUnrecorded(deliver ReplySender) ReplySender {
	return deliver
}

func recordingDeliveriesInto(recordedDeliveries *[]string) func(ReplySender) ReplySender {
	return func(deliver ReplySender) ReplySender {
		return func(ctx context.Context, replyTarget ReplyTarget, reply OutboundReply) (string, error) {
			dispatchID, errorValue := deliver(ctx, replyTarget, reply)
			if errorValue == nil {
				*recordedDeliveries = append(*recordedDeliveries, dispatchID)
			}
			return dispatchID, errorValue
		}
	}
}

func TestAReplyCarryingMoreThanWordsIsSentWhole(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	narrator.observe(context.Background(), acp.StartToolCall("call-a", "file_read(/a)"))

	sent := 0
	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		sent++
		return "message-2", nil
	}, deliveredUnrecorded)
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
	narrator.observe(context.Background(), acp.StartToolCall("call-a", "file_read(/a)"))
	sendReply := narrator.takeOverSending(func(context.Context, ReplyTarget, OutboundReply) (string, error) {
		return "message-2", nil
	}, deliveredUnrecorded)
	sendReply(context.Background(), ReplyTarget{}, OutboundReply{Message: "done"})

	editsBefore := len(adapter.editedMessages)
	narrator.observe(context.Background(), acp.StartToolCall("call-b", "bash(ls)"))

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

	narrator.observe(context.Background(), acp.StartToolCall("call-1", "file_read(/a)"))
	narrator.observe(context.Background(), acp.StartToolCall("call-2", "bash(ls)"))
	narrator.observe(context.Background(), acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
	narrator.observe(context.Background(), acp.UpdateToolCall("call-2", acp.WithUpdateStatus(acp.ToolCallStatusFailed)))

	last := adapter.editedMessages[len(adapter.editedMessages)-1]
	if want := "_file_read(/a) ✓_\n_bash(ls) ✗_"; last != want {
		t.Fatalf("narration reads %q, want %q", last, want)
	}
}

func TestAnUpdateThatSaysNothingOfTheOutcomeChangesNothing(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})
	narrator.observe(context.Background(), acp.StartToolCall("call-1", "file_read(/a)"))
	editsBefore := len(adapter.editedMessages)

	narrator.observe(context.Background(), acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusInProgress)))
	narrator.observe(context.Background(), acp.UpdateToolCall("call-1"))

	if len(adapter.editedMessages) != editsBefore {
		t.Fatal("an update with no outcome edited the narration")
	}
}

func TestAResultForACallNobodyNarratedChangesNothing(t *testing.T) {
	adapter := &recordingNarrationAdapter{sentID: "message-1"}
	narrator := newTurnNarrator(adapter, ReplyTarget{ReplyTargetID: "thread-1"})

	narrator.observe(context.Background(), acp.UpdateToolCall("call-9", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))

	if len(adapter.sentMessages) != 0 || len(adapter.editedMessages) != 0 {
		t.Fatal("a result on its own started a narration")
	}
}

type editingTestAdapter struct {
	*testAdapter
	editedMessages []string
}

func (adapter *editingTestAdapter) EditReply(_ context.Context, _ ReplyTarget, _ string, message string) error {
	adapter.editedMessages = append(adapter.editedMessages, message)
	return nil
}

type externalHarnessDouble struct {
	*harnesstest.Harness
}

func (harness externalHarnessDouble) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	observer := toolcallprogress.ObserverFrom(ctx)
	observer(acp.StartToolCall("call-1", "Read notes.md", acp.WithStartStatus(acp.ToolCallStatusInProgress)))
	observer(acp.UpdateToolCall("call-1", acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
	return harness.Harness.RunTurn(ctx, request)
}

func TestAnExternalHarnessToolCallShowsAsAProgressLine(t *testing.T) {
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	baseHarness := harnesstest.New(taskRunService)
	baseHarness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "done"}
	connectorRuntime, baseAdapter := connectorRuntimeForHarness(t, externalHarnessDouble{baseHarness}, harnessGateway(baseHarness), baseHarness, taskRunService, testLanguageModel{reply: "stub"})
	adapter := &editingTestAdapter{testAdapter: baseAdapter}
	connectorRuntime.RegisterAdapter(adapter)

	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1")); errorValue != nil {
		t.Fatal(errorValue)
	}

	progressLines := []string{}
	for _, reply := range baseAdapter.sentReplies {
		if reply.replyKind == ConnectorReplyKindProgress {
			progressLines = append(progressLines, reply.message)
		}
	}
	if len(progressLines) != 1 || progressLines[0] != "_Read notes.md_" {
		t.Fatalf("progress lines = %q, want the external harness's tool call once", progressLines)
	}
	if last := adapter.editedMessages[len(adapter.editedMessages)-1]; last != "done" {
		t.Fatalf("the progress line ends as %q, want the answer", last)
	}
}
