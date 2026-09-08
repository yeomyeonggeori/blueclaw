package connectors

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/task"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type revisionTestRepository struct {
	testConnectorQueueRepository
	unansweredEvents  []PlatformInboundEvent
	suppressedReplies []QueuedConnectorReply
}

func (repository *revisionTestRepository) ListUnansweredConnectorEvents() ([]PlatformInboundEvent, error) {
	return repository.unansweredEvents, nil
}

func (repository *revisionTestRepository) SuppressConnectorReply(reply QueuedConnectorReply, _ string) error {
	repository.suppressedReplies = append(repository.suppressedReplies, reply)
	return nil
}

func revisionInboundEvent(messageID string, prompt string) PlatformInboundEvent {
	event := testInboundEvent(messageID)
	event.ReplyTargetID = messageID
	event.Prompt = prompt
	return event
}

func TestConnectorRevisionCoalescesQueuedMessagesBeforeLaunch(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "combined result"}
	repository := &revisionTestRepository{}
	connectorRuntime.UseEventRepository(repository)
	for _, event := range []PlatformInboundEvent{
		revisionInboundEvent("first", "Write a summary"),
		revisionInboundEvent("second", "of the design review"),
		revisionInboundEvent("third", "in Korean"),
	} {
		if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	for range 3 {
		connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}
	connectorRuntime.processNextQueuedConnectorReply(context.Background())
	if harness.RunTurnCallCount() != 1 || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one launch and reply, launches=%d replies=%+v", harness.RunTurnCallCount(), adapter.sentReplies)
	}
	request := harness.LastTurnRequest()
	if !strings.Contains(request.Prompt, "Write a summary\n\nof the design review\n\nin Korean") {
		t.Fatalf("expected all message fragments in order, got %q", request.Prompt)
	}
	if request.OriginReplyTargetID != "third" {
		t.Fatalf("expected latest message reply destination, got %q", request.OriginReplyTargetID)
	}
	for _, result := range repository.succeededEvents[:2] {
		if result.Reason != SupersededRequestReason {
			t.Fatalf("expected superseded earlier event, got %+v", result)
		}
	}
}

func TestConnectorRevisionSuppressesCompletedButUndeliveredReply(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "obsolete result"}
	repository := &revisionTestRepository{}
	connectorRuntime.UseEventRepository(repository)
	first := revisionInboundEvent("first", "Create a document")
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, first); errorValue != nil {
		t.Fatal(errorValue)
	}
	connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	previousTask, isFound := connectorRuntime.findTaskRunBySourceReference("person-1", first.DedupeKey())
	if !isFound {
		t.Fatal("expected original task before revision")
	}
	connectorRuntime.taskRunService.AppendTaskEvent(previousTask.TaskRunID, "tool.document.create.result", `{"documentID":"existing-document"}`)
	second := revisionInboundEvent("second", "Use HTML")
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, second); errorValue != nil {
		t.Fatal(errorValue)
	}
	connectorRuntime.processNextQueuedConnectorReply(context.Background())
	if len(adapter.sentReplies) != 0 || len(repository.suppressedReplies) != 1 {
		t.Fatalf("expected obsolete outbox reply suppressed, sent=%+v suppressed=%+v", adapter.sentReplies, repository.suppressedReplies)
	}
	harness.TurnResult.FinishMessage = "revised result"
	connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	connectorRuntime.processNextQueuedConnectorReply(context.Background())
	if len(adapter.sentReplies) != 1 || adapter.sentReplies[0].message != "revised result" {
		t.Fatalf("expected only revised reply, got %+v", adapter.sentReplies)
	}
	priorTask := harness.LastTurnRequest().PriorTask
	if priorTask.TaskRunID != previousTask.TaskRunID || !strings.Contains(priorTask.Result, "existing-document") {
		t.Fatalf("expected recorded effect available to the replacement, got %+v", priorTask)
	}
	if priorTask.Prompt != harness.LastTurnRequest().Prompt || agentcontract.OutcomeContractHasRequirements(priorTask.OutcomeContract) {
		t.Fatalf("expected revised prompt without obsolete outcome requirements, got %+v", priorTask)
	}
}

func TestConnectorRevisionCancelsRunningTaskBeforeLaunchingReplacement(t *testing.T) {
	languageModel := &blockingTestLanguageModel{reply: "final result", started: make(chan struct{}), release: make(chan struct{})}
	connectorRuntime, adapter := newTestConnectorRuntime(t, languageModel)
	connectorRuntimeAgentKernel(connectorRuntime).UseIntakeLanguageModelProvider(testLanguageModel{reply: "classified"})
	repository := &revisionTestRepository{}
	connectorRuntime.UseEventRepository(repository)
	first := revisionInboundEvent("first", "Write a report")
	if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, first); errorValue != nil {
		t.Fatal(errorValue)
	}
	finished := make(chan struct{})
	go func() {
		connectorRuntime.processNextQueuedConnectorEvent(context.Background())
		close(finished)
	}()
	select {
	case <-languageModel.started:
	case <-time.After(5 * time.Second):
		t.Fatal("initial task did not reach the model")
	}
	for _, event := range []PlatformInboundEvent{revisionInboundEvent("second", "as HTML"), revisionInboundEvent("third", "with a short introduction")} {
		if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		close(languageModel.release)
		t.Fatal("superseded task did not stop after cancellation")
	}
	close(languageModel.release)
	previousTask, isFound := connectorRuntime.findTaskRunBySourceReference("person-1", first.DedupeKey())
	if !isFound || previousTask.Status != task.TaskStatusCancelled {
		t.Fatalf("expected initial task cancelled, found=%v task=%+v", isFound, previousTask)
	}
	for range 2 {
		connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}
	for connectorRuntime.processNextQueuedConnectorReply(context.Background()) {
	}
	if len(adapter.sentReplies) != 1 {
		t.Fatalf("expected one final reply, got %+v", adapter.sentReplies)
	}
}

func TestConnectorRevisionRestoreRetainsPersistedMessageHistory(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "final result"}
	first := revisionInboundEvent("first", "Summarize the meeting")
	second := revisionInboundEvent("second", "in Korean")
	second.PreviousMessages = []PendingRequestMessage{pendingMessageFromEvent(first)}
	repository := &revisionTestRepository{unansweredEvents: []PlatformInboundEvent{first, second}}
	repository.pendingEvents = []QueuedConnectorEvent{{Event: first}, {Event: second}}
	connectorRuntime.UseEventRepository(repository)
	if errorValue := connectorRuntime.restorePendingRequests(); errorValue != nil {
		t.Fatal(errorValue)
	}
	for range 2 {
		connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}
	connectorRuntime.processNextQueuedConnectorReply(context.Background())
	if harness.RunTurnCallCount() != 1 || strings.Count(harness.LastTurnRequest().Prompt, first.Prompt) != 1 || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected restored latest request once, launches=%d prompt=%q replies=%+v", harness.RunTurnCallCount(), harness.LastTurnRequest().Prompt, adapter.sentReplies)
	}
}

func TestConnectorRevisionUsesLatestEditedMessageOnce(t *testing.T) {
	connectorRuntime, adapter, harness := newStubbedTestConnectorRuntime(t)
	harness.TurnDecision = startTaskTurnDecision()
	harness.TurnResult = agentcontract.AgentTurnResult{FinishMessage: "latest version"}
	repository := &revisionTestRepository{}
	connectorRuntime.UseEventRepository(repository)
	original := revisionInboundEvent("original", "Write a PDF report")
	firstEdit := original
	firstEdit.EventID = "edit-first"
	firstEdit.Prompt = "Write an HTML report"
	lastEdit := original
	lastEdit.EventID = "edit-last"
	lastEdit.Prompt = "Write a plain text report"
	for _, event := range []PlatformInboundEvent{original, firstEdit, lastEdit} {
		if _, errorValue := connectorRuntime.HandleInboundEvent(context.Background(), adapter, event); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	for range 3 {
		connectorRuntime.processNextQueuedConnectorEvent(context.Background())
	}
	connectorRuntime.processNextQueuedConnectorReply(context.Background())
	request := harness.LastTurnRequest()
	if harness.RunTurnCallCount() != 1 || len(adapter.sentReplies) != 1 {
		t.Fatalf("expected edited request once, launches=%d replies=%+v", harness.RunTurnCallCount(), adapter.sentReplies)
	}
	if strings.Contains(request.Prompt, original.Prompt) || strings.Contains(request.Prompt, firstEdit.Prompt) || !strings.Contains(request.Prompt, lastEdit.Prompt) {
		t.Fatalf("expected only current version of edited message, got %q", request.Prompt)
	}
	if request.OriginReplyTargetID != original.ReplyTargetID {
		t.Fatalf("expected reply at original message, got %q", request.OriginReplyTargetID)
	}
	if original.DedupeKey() == firstEdit.DedupeKey() || firstEdit.DedupeKey() == lastEdit.DedupeKey() {
		t.Fatal("expected independent delivery identities for edits")
	}
}
