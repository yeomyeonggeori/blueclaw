package connectors

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func pendingRequestTestEvent(messageID string) PlatformInboundEvent {
	return PlatformInboundEvent{
		Platform:       "mattermost",
		ConversationID: "channel-1",
		MessageID:      messageID,
		SenderID:       "sender-1",
		ReplyTargetID:  messageID,
		Prompt:         messageID,
	}
}

func TestPendingRequestStoreCollectsSequentialRootMessages(t *testing.T) {
	store := newPendingRequestStore()
	contextValue := context.Background()
	firstEvent := pendingRequestTestEvent("message-1")
	secondEvent := pendingRequestTestEvent("message-2")
	thirdEvent := pendingRequestTestEvent("message-3")

	_, firstRequest, startReason := store.begin(contextValue, firstEvent)
	if startReason != pendingRequestStarted {
		t.Fatal("expected first request to begin")
	}
	store.finish(firstRequest)
	_, secondRequest, startReason := store.begin(contextValue, secondEvent)
	if startReason != pendingRequestStarted {
		t.Fatal("expected second request to begin")
	}
	store.finish(secondRequest)
	_, thirdRequest, startReason := store.begin(contextValue, thirdEvent)
	if startReason != pendingRequestStarted {
		t.Fatal("expected third request to begin")
	}

	if len(thirdRequest.event.PreviousMessages) != 2 {
		t.Fatalf("expected two previous messages, got %+v", thirdRequest.event.PreviousMessages)
	}
	if thirdRequest.event.PreviousMessages[0].Prompt != firstEvent.Prompt || thirdRequest.event.PreviousMessages[1].Prompt != secondEvent.Prompt {
		t.Fatalf("expected oldest-to-newest previous messages, got %+v", thirdRequest.event.PreviousMessages)
	}
}

func TestPendingRequestStoreSupersededEntriesCannotBegin(t *testing.T) {
	store := newPendingRequestStore()
	firstContext, firstRequest, startReason := store.begin(context.Background(), pendingRequestTestEvent("message-1"))
	if startReason != pendingRequestStarted {
		t.Fatal("expected first request to begin")
	}
	_, secondRequest, startReason := store.begin(context.Background(), pendingRequestTestEvent("message-2"))
	if startReason != pendingRequestStarted {
		t.Fatal("expected second request to begin")
	}
	if !store.isSuperseded(firstRequest.event.DedupeKey()) {
		t.Fatal("expected first request to be superseded")
	}
	select {
	case <-firstContext.Done():
	default:
		t.Fatal("expected superseded running request context to be cancelled")
	}
	_, _, startReason = store.begin(context.Background(), pendingRequestTestEvent("message-1"))
	if startReason == pendingRequestStarted {
		t.Fatal("expected superseded entry not to begin")
	}
	store.finish(secondRequest)
}

func TestPendingRequestStoreDoesNotSupersedeDifferentScopes(t *testing.T) {
	cases := []struct {
		name   string
		modify func(PlatformInboundEvent) PlatformInboundEvent
	}{
		{name: "sender", modify: func(event PlatformInboundEvent) PlatformInboundEvent { event.SenderID = "sender-2"; return event }},
		{name: "platform", modify: func(event PlatformInboundEvent) PlatformInboundEvent { event.Platform = "slack"; return event }},
		{name: "channel", modify: func(event PlatformInboundEvent) PlatformInboundEvent { event.ConversationID = "channel-2"; return event }},
		{name: "thread", modify: func(event PlatformInboundEvent) PlatformInboundEvent { event.MessageID = "message-2"; event.ReplyTargetID = "root-2"; return event }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newPendingRequestStore()
			firstEvent := pendingRequestTestEvent("message-1")
			_, firstRequest, startReason := store.begin(context.Background(), firstEvent)
			if startReason != pendingRequestStarted {
				t.Fatal("expected first request to begin")
			}
			secondEvent := testCase.modify(pendingRequestTestEvent("message-2"))
			_, secondRequest, startReason := store.begin(context.Background(), secondEvent)
			if startReason != pendingRequestStarted {
				t.Fatal("expected different-scope request to begin")
			}
			if firstRequest.isSuperseded {
				t.Fatal("expected different-scope request to leave first request active")
			}
			if len(secondRequest.event.PreviousMessages) != 0 {
				t.Fatalf("expected no previous messages across scopes, got %+v", secondRequest.event.PreviousMessages)
			}
		})
	}
}

func TestPendingRequestStoreThreadAndRootScopes(t *testing.T) {
	store := newPendingRequestStore()
	rootEvent := pendingRequestTestEvent("root-message")
	_, rootRequest, startReason := store.begin(context.Background(), rootEvent)
	if startReason != pendingRequestStarted {
		t.Fatal("expected root request to begin")
	}
	store.finish(rootRequest)

	threadEvent := pendingRequestTestEvent("thread-message")
	threadEvent.ReplyTargetID = rootEvent.MessageID
	_, threadRequest, startReason := store.begin(context.Background(), threadEvent)
	if startReason != pendingRequestStarted {
		t.Fatal("expected same-root thread request to begin")
	}
	if !rootRequest.isSuperseded {
		t.Fatal("expected same-root thread request to supersede root request")
	}
	store.finish(threadRequest)

	otherThreadEvent := pendingRequestTestEvent("other-thread-message")
	otherThreadEvent.ReplyTargetID = "other-root"
	_, _, startReason = store.begin(context.Background(), otherThreadEvent)
	if startReason != pendingRequestStarted {
		t.Fatal("expected other-thread request to begin")
	}
	if threadRequest.isSuperseded {
		t.Fatal("expected other-thread request not to supersede same-root request")
	}
}

func TestPendingRequestStoreDeliveredReplyClosesWindow(t *testing.T) {
	store := newPendingRequestStore()
	_, request, startReason := store.begin(context.Background(), pendingRequestTestEvent("message-1"))
	if startReason != pendingRequestStarted {
		t.Fatal("expected request to begin")
	}
	store.mutex.Lock()
	request.hasReply = true
	store.mutex.Unlock()
	store.finish(request)

	_, nextRequest, startReason := store.begin(context.Background(), pendingRequestTestEvent("message-2"))
	if startReason != pendingRequestStarted {
		t.Fatal("expected next request to begin")
	}
	if len(nextRequest.event.PreviousMessages) != 0 {
		t.Fatalf("expected delivered reply to close revision window, got %+v", nextRequest.event.PreviousMessages)
	}
}

func TestPendingRequestStoreDuplicateIDDoesNotSupersede(t *testing.T) {
	store := newPendingRequestStore()
	event := pendingRequestTestEvent("message-1")
	_, firstRequest, startReason := store.begin(context.Background(), event)
	if startReason != pendingRequestStarted {
		t.Fatal("expected first request to begin")
	}
	_, duplicateRequest, startReason := store.begin(context.Background(), event)
	if startReason == pendingRequestStarted || duplicateRequest != firstRequest {
		t.Fatal("expected duplicate request to reuse the existing entry")
	}
	if firstRequest.isSuperseded {
		t.Fatal("expected duplicate ID not to supersede request")
	}
}

func TestPendingRequestStoreReportsAlreadyRunningDuplicate(t *testing.T) {
	store := newPendingRequestStore()
	event := pendingRequestTestEvent("message-1")
	_, request, startReason := store.begin(context.Background(), event)
	if startReason != pendingRequestStarted {
		t.Fatalf("expected first request to start, got %q", startReason)
	}
	_, duplicateRequest, startReason := store.begin(context.Background(), event)
	if startReason != pendingRequestAlreadyRunning {
		t.Fatalf("expected duplicate request to report already running, got %q", startReason)
	}
	if duplicateRequest != request {
		t.Fatal("expected duplicate request to reuse the running entry")
	}
	if !shouldDeferQueuedConnectorEvent(ConnectorRuntimeResult{Reason: requestAlreadyRunningReason}) {
		t.Fatal("expected already-running result to defer queued event")
	}
	store.finish(request)
}

func TestRevisedRequestEventPreservesInputPartsAndAttachments(t *testing.T) {
	firstPart := agentcontract.AgentPart{Type: agentcontract.AgentPartTypeText, Text: "first part"}
	currentPart := agentcontract.AgentPart{Type: agentcontract.AgentPartTypeFile, File: &agentcontract.AgentFilePart{Filename: "current.txt"}}
	firstAttachment := InputAttachment{FileID: "file-1", Filename: "first.txt"}
	currentAttachment := InputAttachment{FileID: "file-2", Filename: "current.txt"}
	event := pendingRequestTestEvent("message-2")
	event.Prompt = "current prompt"
	event.InputParts = []agentcontract.AgentPart{currentPart}
	event.Context.InputAttachments = []InputAttachment{currentAttachment}
	event.PreviousMessages = []PendingRequestMessage{{Prompt: "first prompt", InputParts: []agentcontract.AgentPart{firstPart}, InputAttachments: []InputAttachment{firstAttachment}}}

	revised := revisedRequestEvent(event)
	if revised.Prompt != "The following user messages arrived in order before a reply. They form one revised request. Apply later corrections to earlier details and handle the final combined request once.\n\nfirst prompt\n\ncurrent prompt" {
		t.Fatalf("unexpected revised prompt: %q", revised.Prompt)
	}
	if len(revised.InputParts) != 2 || revised.InputParts[0].Text != firstPart.Text || revised.InputParts[1].File == nil {
		t.Fatalf("expected previous and current input parts, got %+v", revised.InputParts)
	}
	if len(revised.Context.InputAttachments) != 2 || revised.Context.InputAttachments[0].FileID != firstAttachment.FileID || revised.Context.InputAttachments[1].FileID != currentAttachment.FileID {
		t.Fatalf("expected previous and current attachments, got %+v", revised.Context.InputAttachments)
	}
}
