package memory

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestMemoryEpisodeFromUpdateJobSetsUniqueMessageID(t *testing.T) {
	job := PrepareMemoryUpdateJob(MemoryUpdateJob{
		Namespace:      MemoryNamespace{NamespaceID: "user:person-1", ScopeType: ScopeTypeUser, ScopePersonID: "person-1"},
		Content:        "Dana prefers rice over noodles.",
		Platform:       "mattermost",
		SenderPersonID: "person-1",
	})
	episode := memoryEpisodeFromUpdateJob(job)
	if episode.MessageID == "" {
		t.Fatal("expected a non-empty source message id so the graphiti_episode unique (source_platform, source_message_id) constraint is not violated by repeated remembers")
	}
	if episode.MessageID != episode.EpisodeID {
		t.Fatalf("expected message id to equal the unique job id, got message=%q episode=%q", episode.MessageID, episode.EpisodeID)
	}
}

func TestBackgroundMemoryUpdateQueueDrainsBufferedJobsBeforeDeadline(t *testing.T) {
	store := &recordingGraphStore{}
	memoryService := &MemoryService{}
	memoryService.UseGraphStore(store)
	queue := NewBackgroundMemoryUpdateQueue(NewMemoryUpdateProcessor(memoryService), nil)
	queue.Start(context.Background())

	for index := 0; index < 5; index++ {
		if _, errorValue := queue.Enqueue(MemoryUpdateJob{
			Namespace:      UserNamespace("person-1"),
			Content:        "fact " + strconv.Itoa(index),
			ConversationID: "conversation-" + strconv.Itoa(index),
		}); errorValue != nil {
			t.Fatal(errorValue)
		}
	}

	drainContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := queue.Drain(drainContext)

	if !result.Drained {
		t.Fatalf("expected the queue to drain before the deadline, got %+v", result)
	}
	if len(result.DroppedJobs) != 0 {
		t.Fatalf("expected no dropped jobs, got %+v", result.DroppedJobs)
	}
	if len(store.episodes) != 5 {
		t.Fatalf("expected every buffered job to reach the graph store, got %d", len(store.episodes))
	}
}

type blockingGraphMemoryStore struct {
	started chan struct{}
	release chan struct{}
}

func (store *blockingGraphMemoryStore) AddEpisode(context.Context, MemoryEpisode) (MemoryIngestionResult, error) {
	select {
	case store.started <- struct{}{}:
	default:
	}
	<-store.release
	return MemoryIngestionResult{}, nil
}

func (store *blockingGraphMemoryStore) SearchFacts(context.Context, MemorySearchRequest) ([]MemoryFact, error) {
	return nil, nil
}

func TestBackgroundMemoryUpdateQueueReportsDroppedJobsPastDeadline(t *testing.T) {
	store := &blockingGraphMemoryStore{started: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(store.release)
	memoryService := &MemoryService{}
	memoryService.UseGraphStore(store)
	queue := NewBackgroundMemoryUpdateQueue(NewMemoryUpdateProcessor(memoryService), nil)
	queue.Start(context.Background())

	if _, errorValue := queue.Enqueue(MemoryUpdateJob{Namespace: UserNamespace("person-1"), Content: "first", ConversationID: "conversation-1"}); errorValue != nil {
		t.Fatal(errorValue)
	}
	<-store.started

	if _, errorValue := queue.Enqueue(MemoryUpdateJob{Namespace: UserNamespace("person-2"), Content: "second", ConversationID: "conversation-2"}); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, errorValue := queue.Enqueue(MemoryUpdateJob{Namespace: UserNamespace("person-3"), Content: "third", ConversationID: "conversation-3"}); errorValue != nil {
		t.Fatal(errorValue)
	}

	drainContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := queue.Drain(drainContext)

	if result.Drained {
		t.Fatalf("expected drain to time out with a job still blocked, got %+v", result)
	}
	if len(result.DroppedJobs) != 3 {
		t.Fatalf("expected all three outstanding jobs reported dropped, got %+v", result.DroppedJobs)
	}
	droppedConversationIDs := map[string]bool{}
	for _, droppedJob := range result.DroppedJobs {
		droppedConversationIDs[droppedJob.ConversationID] = true
	}
	for _, expectedConversationID := range []string{"conversation-1", "conversation-2", "conversation-3"} {
		if !droppedConversationIDs[expectedConversationID] {
			t.Fatalf("expected dropped job for %s, got %+v", expectedConversationID, result.DroppedJobs)
		}
	}
}

func TestBackgroundMemoryUpdateQueueRefusesEnqueueOnceQuiescing(t *testing.T) {
	store := &recordingGraphStore{}
	memoryService := &MemoryService{}
	memoryService.UseGraphStore(store)
	queue := NewBackgroundMemoryUpdateQueue(NewMemoryUpdateProcessor(memoryService), nil)
	queue.Start(context.Background())

	drainContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if result := queue.Drain(drainContext); !result.Drained {
		t.Fatalf("expected an empty queue to drain immediately, got %+v", result)
	}

	if _, errorValue := queue.Enqueue(MemoryUpdateJob{Namespace: UserNamespace("person-1"), Content: "too late"}); errorValue == nil {
		t.Fatal("expected enqueue to be refused once the queue is quiescing")
	}
}
