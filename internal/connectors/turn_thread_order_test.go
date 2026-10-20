//go:build !nobundledharness

package connectors

import (
	"context"
	"slices"
	"strconv"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/blueclaw/internal/toolcallprogress"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract/harnesstest"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type threadPost struct {
	messageID string
	text      string
	replyKind string
	isEdited  bool
}

type threadOperation struct {
	name      string
	messageID string
	text      string
}

type threadRecordingAdapter struct {
	*testAdapter
	posts                 []threadPost
	operations            []threadOperation
	editsOfAnOlderMessage []string
}

func (adapter *threadRecordingAdapter) SendReply(_ context.Context, _ ReplyTarget, reply OutboundReply) (string, error) {
	messageID := "post-" + strconv.Itoa(len(adapter.operations)+1)
	adapter.posts = append(adapter.posts, threadPost{messageID: messageID, text: reply.Message, replyKind: reply.ReplyKind})
	adapter.operations = append(adapter.operations, threadOperation{name: "send", messageID: messageID, text: reply.Message})
	return messageID, nil
}

func (adapter *threadRecordingAdapter) EditReply(_ context.Context, _ ReplyTarget, messageID string, message string) error {
	adapter.operations = append(adapter.operations, threadOperation{name: "edit", messageID: messageID, text: message})
	visible := adapter.visible()
	if len(visible) == 0 || visible[len(visible)-1].messageID != messageID {
		adapter.editsOfAnOlderMessage = append(adapter.editsOfAnOlderMessage, messageID)
	}
	for index := range adapter.posts {
		if adapter.posts[index].messageID == messageID {
			adapter.posts[index].text = message
			adapter.posts[index].isEdited = true
		}
	}
	return nil
}

func (adapter *threadRecordingAdapter) visible() []threadPost {
	return slices.DeleteFunc(slices.Clone(adapter.posts), func(post threadPost) bool { return post.messageID == "" })
}

type deletingThreadRecordingAdapter struct {
	*threadRecordingAdapter
}

func (adapter deletingThreadRecordingAdapter) DeleteReply(_ context.Context, _ ReplyTarget, messageID string) error {
	adapter.operations = append(adapter.operations, threadOperation{name: "delete", messageID: messageID})
	for index := range adapter.posts {
		if adapter.posts[index].messageID == messageID {
			adapter.posts[index].messageID = ""
		}
	}
	return nil
}

type turnStep struct {
	toolLabel  string
	checkpoint string
}

func tool(label string) turnStep         { return turnStep{toolLabel: label} }
func checkpoint(message string) turnStep { return turnStep{checkpoint: message} }

type scriptedThreadHarness struct {
	*harnesstest.Harness
	steps []turnStep
}

func (harness scriptedThreadHarness) RunTurn(ctx context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	observer := toolcallprogress.ObserverFrom(ctx)
	for index, step := range harness.steps {
		if step.checkpoint != "" {
			if errorValue := request.CheckpointSender(ctx, agentcontract.AgentCheckpoint{Message: step.checkpoint}); errorValue != nil {
				return agentcontract.AgentTurnResult{}, errorValue
			}
			continue
		}
		callID := acp.ToolCallId("call-" + strconv.Itoa(index))
		observer(acp.StartToolCall(callID, step.toolLabel, acp.WithStartStatus(acp.ToolCallStatusInProgress)))
		observer(acp.UpdateToolCall(callID, acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
	}
	return harness.Harness.RunTurn(ctx, request)
}

func runScriptedThread(t *testing.T, steps []turnStep, finalAttachments []toolcontract.FileAttachment, canDelete bool) *threadRecordingAdapter {
	t.Helper()
	taskRunService := task.NewTaskRunService(task.NewTaskEventService())
	baseHarness := harnesstest.New(taskRunService)
	baseHarness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "final answer", Attachments: finalAttachments}
	connectorRuntime, baseAdapter := connectorRuntimeForHarness(t, scriptedThreadHarness{Harness: baseHarness, steps: steps}, harnessGateway(baseHarness), baseHarness, taskRunService, testLanguageModel{reply: "stub"})
	recorder := &threadRecordingAdapter{testAdapter: baseAdapter}
	var adapter PlatformAdapter = recorder
	if canDelete {
		adapter = deletingThreadRecordingAdapter{threadRecordingAdapter: recorder}
	}
	connectorRuntime.RegisterAdapter(adapter)
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, testInboundEvent("message-1")); errorValue != nil {
		t.Fatal(errorValue)
	}
	return recorder
}

func visibleTexts(posts []threadPost) []string {
	texts := []string{}
	for _, post := range posts {
		texts = append(texts, post.text)
	}
	return texts
}

func TestATurnReadsInTheOrderItHappened(t *testing.T) {
	cases := []struct {
		name             string
		steps            []turnStep
		finalAttachments []toolcontract.FileAttachment
		canDelete        bool
		wantThread       []string
		wantFinalEdited  bool
	}{
		{
			name:       "checkpoints edit the progress before them and the answer comes last",
			steps:      []turnStep{tool("file_read(/a)"), checkpoint("first look done"), tool("bash(ls)"), checkpoint("second look done"), tool("file_write(/b)")},
			canDelete:  true,
			wantThread: []string{"first look done", "second look done", "final answer"},
		},
		{
			name:       "a turn without a checkpoint takes its progress down",
			steps:      []turnStep{tool("file_read(/a)"), tool("bash(ls)")},
			canDelete:  true,
			wantThread: []string{"final answer"},
		},
		{
			name:            "a platform that cannot delete gets the answer edited into the newest progress",
			steps:           []turnStep{tool("file_read(/a)"), checkpoint("first look done"), tool("bash(ls)")},
			canDelete:       false,
			wantThread:      []string{"first look done", "final answer"},
			wantFinalEdited: true,
		},
		{
			name:             "an answer carrying a file is posted after the checkpoints",
			steps:            []turnStep{tool("file_read(/a)"), checkpoint("first look done"), tool("bash(ls)")},
			finalAttachments: []toolcontract.FileAttachment{{DevicePath: "/tmp/report.pdf", Filename: "report.pdf"}},
			canDelete:        true,
			wantThread:       []string{"first look done", "final answer"},
		},
		{
			name:       "two checkpoints in a row post the second one new",
			steps:      []turnStep{tool("file_read(/a)"), checkpoint("first look done"), checkpoint("still on it")},
			canDelete:  true,
			wantThread: []string{"first look done", "still on it", "final answer"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := runScriptedThread(t, testCase.steps, testCase.finalAttachments, testCase.canDelete)
			visible := adapter.visible()

			if texts := visibleTexts(visible); !slices.Equal(texts, testCase.wantThread) {
				t.Fatalf("the thread reads %q, want %q\noperations: %+v", texts, testCase.wantThread, adapter.operations)
			}
			assertTheAnswerIsTheLastWordAndNothingWasRewrittenBehindIt(t, adapter, visible, testCase.wantFinalEdited)
		})
	}
}

func assertTheAnswerIsTheLastWordAndNothingWasRewrittenBehindIt(t *testing.T, adapter *threadRecordingAdapter, visible []threadPost, isFinalAnEdit bool) {
	t.Helper()
	last := visible[len(visible)-1]
	if last.text != "final answer" {
		t.Fatalf("the last visible message reads %q, want the answer", last.text)
	}
	if last.isEdited != isFinalAnEdit {
		t.Fatalf("the answer arrived as an edit = %t, want %t", last.isEdited, isFinalAnEdit)
	}
	if len(adapter.editsOfAnOlderMessage) > 0 {
		t.Fatalf("messages %q were edited with something already after them", adapter.editsOfAnOlderMessage)
	}
}
