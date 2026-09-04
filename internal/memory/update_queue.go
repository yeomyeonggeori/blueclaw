package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultMemoryUpdateQueueSize = 128

type MemoryUpdateJob struct {
	JobID           string
	Namespace       MemoryNamespace
	Content         string
	Platform        string
	ConversationID  string
	SenderPersonID  string
	SourceReference string
	OccurredAt      time.Time
}

type MemoryUpdateAccepted struct {
	Accepted         bool   `json:"accepted"`
	JobID            string `json:"jobID,omitempty"`
	Status           string `json:"status"`
	Durability       string `json:"durability"`
	GraphitiStatus   string `json:"graphitiStatus,omitempty"`
	MarkdownUpdated  bool   `json:"markdownUpdated,omitempty"`
	FailureCode      string `json:"failureCode,omitempty"`
	FailureComponent string `json:"failureComponent,omitempty"`
}

type MemoryUpdateEnqueuer interface {
	Enqueue(MemoryUpdateJob) (MemoryUpdateAccepted, error)
}

type MemoryUpdateProcessor struct {
	memoryService *MemoryService
}

type MemoryUpdateResult struct {
	GraphitiSucceeded bool
	GraphitiError     string
}

type MemoryQueueDrainResult struct {
	Drained     bool
	DroppedJobs []MemoryUpdateJob
}

type BackgroundMemoryUpdateQueue struct {
	processor   MemoryUpdateProcessor
	jobs        chan MemoryUpdateJob
	logger      *slog.Logger
	quiescing   atomic.Bool
	pendingJobs sync.WaitGroup
	currentJob  currentMemoryUpdateJob
}

type currentMemoryUpdateJob struct {
	mutex  sync.Mutex
	job    MemoryUpdateJob
	active bool
}

func (current *currentMemoryUpdateJob) set(job MemoryUpdateJob) {
	current.mutex.Lock()
	defer current.mutex.Unlock()
	current.job = job
	current.active = true
}

func (current *currentMemoryUpdateJob) clear() {
	current.mutex.Lock()
	defer current.mutex.Unlock()
	current.active = false
}

func (current *currentMemoryUpdateJob) snapshot() (MemoryUpdateJob, bool) {
	current.mutex.Lock()
	defer current.mutex.Unlock()
	return current.job, current.active
}

func NewMemoryUpdateProcessor(memoryService *MemoryService) MemoryUpdateProcessor {
	return MemoryUpdateProcessor{
		memoryService: memoryService,
	}
}

func NewBackgroundMemoryUpdateQueue(processor MemoryUpdateProcessor, logger *slog.Logger) *BackgroundMemoryUpdateQueue {
	return &BackgroundMemoryUpdateQueue{
		processor: processor,
		jobs:      make(chan MemoryUpdateJob, defaultMemoryUpdateQueueSize),
		logger:    loggerOrDefault(logger),
	}
}

func (queue *BackgroundMemoryUpdateQueue) Start(ctx context.Context) {
	if queue == nil {
		return
	}
	go queue.run(ctx)
}

func (queue *BackgroundMemoryUpdateQueue) Enqueue(job MemoryUpdateJob) (MemoryUpdateAccepted, error) {
	if queue == nil {
		return MemoryUpdateAccepted{}, errors.New("memory update queue is unavailable")
	}
	if queue.quiescing.Load() {
		return MemoryUpdateAccepted{}, errors.New("memory update queue is quiescing for shutdown")
	}
	normalizedJob := normalizeMemoryUpdateJob(job)
	queue.pendingJobs.Add(1)
	// TODO: Replace the volatile in-process memory update queue with a durable queue.
	select {
	case queue.jobs <- normalizedJob:
		return MemoryUpdateAccepted{
			Accepted:       true,
			JobID:          normalizedJob.JobID,
			Status:         "queued_volatile",
			Durability:     "volatile",
			GraphitiStatus: "queued",
		}, nil
	default:
		queue.pendingJobs.Done()
		return MemoryUpdateAccepted{}, errors.New("memory update queue is full")
	}
}

// Drain stops the queue from accepting new jobs and waits, until ctx is
// done, for the in-flight job and every buffered job to finish processing.
// Jobs still outstanding when ctx is done are reported as dropped rather
// than silently discarded.
func (queue *BackgroundMemoryUpdateQueue) Drain(ctx context.Context) MemoryQueueDrainResult {
	if queue == nil {
		return MemoryQueueDrainResult{Drained: true}
	}
	queue.quiescing.Store(true)
	drained := make(chan struct{})
	go func() {
		queue.pendingJobs.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return MemoryQueueDrainResult{Drained: true}
	case <-ctx.Done():
		return MemoryQueueDrainResult{Drained: false, DroppedJobs: queue.outstandingJobs()}
	}
}

func (queue *BackgroundMemoryUpdateQueue) outstandingJobs() []MemoryUpdateJob {
	outstanding := make([]MemoryUpdateJob, 0, len(queue.jobs)+1)
	if job, active := queue.currentJob.snapshot(); active {
		outstanding = append(outstanding, job)
	}
	for {
		select {
		case job := <-queue.jobs:
			outstanding = append(outstanding, job)
		default:
			return outstanding
		}
	}
}

func (queue *BackgroundMemoryUpdateQueue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-queue.jobs:
			queue.processJob(ctx, job)
		}
	}
}

func (queue *BackgroundMemoryUpdateQueue) processJob(ctx context.Context, job MemoryUpdateJob) {
	queue.currentJob.set(job)
	defer func() {
		queue.currentJob.clear()
		queue.pendingJobs.Done()
		if recovered := recover(); recovered != nil {
			queue.logger.Error("memory.update_panic", slog.String("jobID", job.JobID), slog.Any("panic", recovered))
		}
	}()
	result := queue.processor.Process(context.WithoutCancel(ctx), job)
	queue.logResult(job, result)
}

func (queue *BackgroundMemoryUpdateQueue) logResult(job MemoryUpdateJob, result MemoryUpdateResult) {
	if result.GraphitiError != "" {
		queue.logger.Warn("memory.graphiti_update_failed", slog.String("jobID", job.JobID), slog.String("error", result.GraphitiError))
	} else if result.GraphitiSucceeded {
		queue.logger.Info("memory.graphiti_update_succeeded", slog.String("jobID", job.JobID), slog.String("namespaceID", job.Namespace.NamespaceID))
	}
}

func (processor MemoryUpdateProcessor) Process(ctx context.Context, job MemoryUpdateJob) MemoryUpdateResult {
	normalizedJob := normalizeMemoryUpdateJob(job)
	result := MemoryUpdateResult{}
	if processor.memoryService != nil {
		if _, errorValue := processor.memoryService.AddEpisode(ctx, memoryEpisodeFromUpdateJob(normalizedJob)); errorValue != nil {
			result.GraphitiError = errorValue.Error()
		} else {
			result.GraphitiSucceeded = true
		}
	}
	return result
}

func PrepareMemoryUpdateJob(job MemoryUpdateJob) MemoryUpdateJob {
	return normalizeMemoryUpdateJob(job)
}

func memoryEpisodeFromUpdateJob(job MemoryUpdateJob) MemoryEpisode {
	occurredAt := job.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return MemoryEpisode{
		EpisodeID:       job.JobID,
		Platform:        job.Platform,
		MessageID:       job.JobID,
		ConversationID:  job.ConversationID,
		SenderPersonID:  job.SenderPersonID,
		Prompt:          job.Content,
		OccurredAt:      occurredAt,
		Namespaces:      []MemoryNamespace{job.Namespace},
		Source:          "memory_remember",
		SourceReference: job.SourceReference,
	}
}

func normalizeMemoryUpdateJob(job MemoryUpdateJob) MemoryUpdateJob {
	job.JobID = firstNonEmptyMemoryString(strings.TrimSpace(job.JobID), newMemoryUpdateJobID())
	job.Content = strings.TrimSpace(job.Content)
	if job.OccurredAt.IsZero() {
		job.OccurredAt = time.Now().UTC()
	}
	return job
}

func newMemoryUpdateJobID() string {
	buffer := make([]byte, 12)
	if _, errorValue := rand.Read(buffer); errorValue == nil {
		return "memory-update-" + hex.EncodeToString(buffer)
	}
	return "memory-update-" + time.Now().UTC().Format("20060102150405.000000000")
}

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

func firstNonEmptyMemoryString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
